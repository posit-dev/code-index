// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MarkdownParser extracts structured information from Markdown and Quarto files
// using regex/line-based parsing. Each heading becomes a searchable section
// (mapped to FunctionInfo), and YAML front matter provides file-level metadata.
type MarkdownParser struct {
	srcRoot  string
	excludes []string
}

// NewMarkdownParser creates a new Markdown/Quarto parser.
func NewMarkdownParser(srcRoot string, excludes []string) *MarkdownParser {
	return &MarkdownParser{
		srcRoot:  srcRoot,
		excludes: excludes,
	}
}

// Patterns for markdown extraction.
var (
	// Matches ATX headings: # Heading, ## Heading, etc.
	mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	// Matches YAML front matter delimiters.
	mdFrontMatterDelim = regexp.MustCompile(`^---\s*$`)
	// Matches code fence start/end.
	mdCodeFence = regexp.MustCompile("^```")
	// Patterns for stripping markdown formatting from content.
	mdBold       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalic     = regexp.MustCompile(`\*([^*]+)\*`)
	mdInlineCode = regexp.MustCompile("`([^`]+)`")
	mdLink       = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdImage      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
)

// Parse walks the source tree and extracts all file and section information.
func (p *MarkdownParser) Parse(result *ParseResult) error {
	return filepath.Walk(p.srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			// Skip hidden directories and common build output directories.
			if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".qmd" {
			return nil
		}

		relPath, err := filepath.Rel(p.srcRoot, path)
		if err != nil {
			return nil
		}

		// Check excludes.
		for _, pattern := range p.excludes {
			if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
				return nil
			}
			if matched, err := filepath.Match(pattern, filepath.Base(path)); err == nil && matched {
				return nil
			}
		}

		return p.parseFile(path, relPath, result)
	})
}

// frontMatter holds parsed YAML front matter fields.
type frontMatter struct {
	title       string
	description string
}

// parseFile parses a single Markdown/Quarto file.
func (p *MarkdownParser) parseFile(path, relPath string, result *ParseResult) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	text := string(content)
	lines := strings.Split(text, "\n")

	dir := filepath.Dir(relPath)
	importPath := filepath.ToSlash(dir)
	filePath := filepath.ToSlash(relPath)

	// Parse YAML front matter.
	fm, fmEndLine := parseFrontMatter(lines)

	// Build file-level doc from front matter or first paragraph.
	fileDoc := ""
	if fm.description != "" {
		fileDoc = fm.description
	} else {
		fileDoc = extractFirstParagraph(lines, fmEndLine)
	}

	fileInfo := &FileInfo{
		Path:       filePath,
		Package:    filepath.Base(dir),
		ImportPath: importPath,
		Doc:        fileDoc,
		ASTHash:    hashString(text),
	}

	// Extract sections from headings.
	sections := extractSections(lines, filePath)
	fileInfo.Functions = sections

	if len(fileInfo.Functions) > 0 || fm.title != "" {
		result.Files[filePath] = fileInfo

		if _, ok := result.Packages[importPath]; !ok {
			result.Packages[importPath] = &PackageInfo{
				ImportPath: importPath,
				Dir:        dir,
			}
		}
		result.Packages[importPath].Files = append(result.Packages[importPath].Files, filePath)
	}

	return nil
}

// parseFrontMatter extracts title and description from YAML front matter.
// Returns the parsed fields and the line index where front matter ends (0 if none).
func parseFrontMatter(lines []string) (frontMatter, int) {
	fm := frontMatter{}
	if len(lines) == 0 || !mdFrontMatterDelim.MatchString(lines[0]) {
		return fm, 0
	}

	for i := 1; i < len(lines); i++ {
		if mdFrontMatterDelim.MatchString(lines[i]) {
			return fm, i + 1
		}
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "title:") {
			fm.title = stripYAMLValue(strings.TrimPrefix(line, "title:"))
		}
		if strings.HasPrefix(line, "description:") {
			fm.description = stripYAMLValue(strings.TrimPrefix(line, "description:"))
		}
		if strings.HasPrefix(line, "description-meta:") {
			fm.description = stripYAMLValue(strings.TrimPrefix(line, "description-meta:"))
		}
	}
	return fm, len(lines)
}

// stripYAMLValue cleans a YAML value string by trimming whitespace and quotes.
func stripYAMLValue(s string) string {
	s = strings.TrimSpace(s)
	// Remove surrounding quotes.
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = s[1 : len(s)-1]
		}
	}
	return s
}

// extractFirstParagraph extracts the first non-empty paragraph after front matter.
func extractFirstParagraph(lines []string, startLine int) string {
	var para []string
	started := false
	for i := startLine; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			if started {
				break
			}
			continue
		}
		// Skip headings, code fences, and Quarto directives.
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") || strings.HasPrefix(line, ":::") {
			if started {
				break
			}
			continue
		}
		started = true
		para = append(para, line)
	}
	result := strings.Join(para, " ")
	result = stripMarkdownFormatting(result)
	if len(result) > 300 {
		result = result[:300] + "..."
	}
	return result
}

// extractSections parses headings and their content into FunctionInfo entries.
func extractSections(lines []string, filePath string) []FunctionInfo {
	var sections []FunctionInfo
	inCodeFence := false
	inFrontMatter := false

	// Track current section being built.
	var currentName string
	var currentSig string
	var currentLine int
	var contentLines []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Handle front matter (skip it).
		if i == 0 && mdFrontMatterDelim.MatchString(trimmed) {
			inFrontMatter = true
			continue
		}
		if inFrontMatter {
			if mdFrontMatterDelim.MatchString(trimmed) {
				inFrontMatter = false
			}
			continue
		}

		// Track code fences.
		if mdCodeFence.MatchString(trimmed) {
			inCodeFence = !inCodeFence
			continue
		}
		if inCodeFence {
			continue
		}

		// Check for heading.
		match := mdHeading.FindStringSubmatch(trimmed)
		if match != nil {
			// Save previous section if any.
			if currentName != "" {
				doc := buildSectionDoc(contentLines)
				sections = append(sections, FunctionInfo{
					Name:      currentName,
					Signature: currentSig,
					Doc:       doc,
					File:      filePath,
					Line:      currentLine,
					Exported:  true,
					ASTHash:   hashString(currentSig + doc),
					SigHash:   hashString(currentSig),
				})
			}

			// Start new section.
			currentName = strings.TrimSpace(match[2])
			currentSig = strings.TrimSpace(match[0])
			currentLine = i + 1 // 1-based
			contentLines = nil
			continue
		}

		// Accumulate content for current section.
		if currentName != "" {
			contentLines = append(contentLines, line)
		}
	}

	// Save final section.
	if currentName != "" {
		doc := buildSectionDoc(contentLines)
		sections = append(sections, FunctionInfo{
			Name:      currentName,
			Signature: currentSig,
			Doc:       doc,
			File:      filePath,
			Line:      currentLine,
			Exported:  true,
			ASTHash:   hashString(currentSig + doc),
			SigHash:   hashString(currentSig),
		})
	}

	return sections
}

// buildSectionDoc builds a doc string from section content lines.
// Strips markdown formatting, skips code blocks, and truncates to ~300 chars.
func buildSectionDoc(lines []string) string {
	var parts []string
	inCodeFence := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip code fences and their content.
		if mdCodeFence.MatchString(trimmed) {
			inCodeFence = !inCodeFence
			continue
		}
		if inCodeFence {
			continue
		}

		// Skip empty lines, Quarto directives, and HTML comments.
		if trimmed == "" || strings.HasPrefix(trimmed, ":::") || strings.HasPrefix(trimmed, "<!--") {
			continue
		}

		parts = append(parts, trimmed)
	}

	result := strings.Join(parts, " ")
	result = stripMarkdownFormatting(result)

	if len(result) > 300 {
		result = result[:300] + "..."
	}
	return result
}

// stripMarkdownFormatting removes common markdown formatting from text.
func stripMarkdownFormatting(s string) string {
	// Replace images first (before links, since images contain link syntax).
	s = mdImage.ReplaceAllString(s, "$1")
	// Replace links [text](url) with just text.
	s = mdLink.ReplaceAllString(s, "$1")
	// Replace bold **text** with text.
	s = mdBold.ReplaceAllString(s, "$1")
	// Replace italic *text* with text.
	s = mdItalic.ReplaceAllString(s, "$1")
	// Replace inline code `text` with text.
	s = mdInlineCode.ReplaceAllString(s, "$1")
	return s
}
