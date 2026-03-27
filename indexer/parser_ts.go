// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// TSParser extracts structured information from TypeScript/JavaScript source files using tree-sitter.
type TSParser struct {
	srcRoot  string
	excludes []string
	lang     *sitter.Language
}

// NewTSParser creates a new TypeScript/JavaScript parser.
func NewTSParser(srcRoot string, excludes []string) *TSParser {
	return &TSParser{
		srcRoot:  srcRoot,
		excludes: excludes,
		lang:     typescript.GetLanguage(),
	}
}

// Parse walks the source tree and extracts all file, function, and type information.
func (p *TSParser) Parse(result *ParseResult) error {
	return filepath.Walk(p.srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == "node_modules" || base == "dist" || base == "coverage" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Handle .vue, .ts, .tsx, .js, .jsx files.
		ext := filepath.Ext(path)
		if ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".jsx" && ext != ".vue" {
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

		return p.parseFile(path, relPath, ext, result)
	})
}

// parseFile parses a single TS/JS/Vue file.
func (p *TSParser) parseFile(path, relPath, ext string, result *ParseResult) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil // Skip files we can't read.
	}

	// For Vue files, extract the <script> block.
	if ext == ".vue" {
		content = extractVueScript(content)
		if content == nil {
			return nil // No script block found.
		}
	}

	parser := sitter.NewParser()
	parser.SetLanguage(p.lang)

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", relPath, err)
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()

	// Determine the package/module path for this file.
	dir := filepath.Dir(relPath)
	importPath := filepath.ToSlash(dir)

	fileInfo := &FileInfo{
		Path:       filepath.ToSlash(relPath),
		Package:    filepath.Base(dir),
		ImportPath: importPath,
	}

	// Walk the tree and extract declarations.
	p.extractDeclarations(root, content, fileInfo)

	// Compute file hash.
	fileInfo.ASTHash = hashString(string(content))

	if len(fileInfo.Functions) > 0 || len(fileInfo.Types) > 0 {
		result.Files[fileInfo.Path] = fileInfo

		// Build package info.
		if _, ok := result.Packages[importPath]; !ok {
			result.Packages[importPath] = &PackageInfo{
				ImportPath: importPath,
				Dir:        dir,
			}
		}
		result.Packages[importPath].Files = append(result.Packages[importPath].Files, fileInfo.Path)
	}

	return nil
}

// extractDeclarations walks the tree-sitter AST and extracts function/type declarations.
func (p *TSParser) extractDeclarations(node *sitter.Node, content []byte, fileInfo *FileInfo) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		nodeType := child.Type()

		switch nodeType {
		case "function_declaration":
			if fn := p.extractFunction(child, content, fileInfo.Path); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}

		case "export_statement":
			// Look inside export statements for declarations.
			p.extractFromExport(child, content, fileInfo)

		case "class_declaration":
			if t := p.extractClass(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "interface_declaration":
			if t := p.extractInterface(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "type_alias_declaration":
			if t := p.extractTypeAlias(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "enum_declaration":
			if t := p.extractEnum(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "lexical_declaration":
			// Handle `const foo = () => {}` and `const foo = function() {}`
			fns := p.extractArrowFunctions(child, content, fileInfo.Path)
			fileInfo.Functions = append(fileInfo.Functions, fns...)
		}
	}
}

// extractFromExport handles export statements that wrap declarations.
func (p *TSParser) extractFromExport(node *sitter.Node, content []byte, fileInfo *FileInfo) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "function_declaration":
			if fn := p.extractFunction(child, content, fileInfo.Path); fn != nil {
				fn.Exported = true
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}
		case "class_declaration":
			if t := p.extractClass(child, content, fileInfo.Path); t != nil {
				t.Exported = true
				fileInfo.Types = append(fileInfo.Types, *t)
			}
		case "interface_declaration":
			if t := p.extractInterface(child, content, fileInfo.Path); t != nil {
				t.Exported = true
				fileInfo.Types = append(fileInfo.Types, *t)
			}
		case "type_alias_declaration":
			if t := p.extractTypeAlias(child, content, fileInfo.Path); t != nil {
				t.Exported = true
				fileInfo.Types = append(fileInfo.Types, *t)
			}
		case "enum_declaration":
			if t := p.extractEnum(child, content, fileInfo.Path); t != nil {
				t.Exported = true
				fileInfo.Types = append(fileInfo.Types, *t)
			}
		case "lexical_declaration":
			fns := p.extractArrowFunctions(child, content, fileInfo.Path)
			for j := range fns {
				fns[j].Exported = true
			}
			fileInfo.Functions = append(fileInfo.Functions, fns...)
		}
	}
}

// extractFunction extracts a function declaration.
func (p *TSParser) extractFunction(node *sitter.Node, content []byte, filePath string) *FunctionInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	line := int(node.StartPoint().Row) + 1

	sig := strings.TrimSpace(extractSignature(node, content))
	doc := extractPrecedingComment(node, content)

	return &FunctionInfo{
		Name:      name,
		Signature: sig,
		Doc:       doc,
		File:      filePath,
		Line:      line,
		Exported:  isExportedTS(name),
		ASTHash:   hashString(node.Content(content)),
		SigHash:   hashString(sig),
	}
}

// extractClass extracts a class declaration.
func (p *TSParser) extractClass(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	doc := extractPrecedingComment(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     "class",
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: isExportedTS(name),
		ASTHash:  hashString(node.Content(content)),
	}
}

// extractInterface extracts an interface declaration.
func (p *TSParser) extractInterface(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	doc := extractPrecedingComment(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     "interface",
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: isExportedTS(name),
		ASTHash:  hashString(node.Content(content)),
	}
}

// extractTypeAlias extracts a type alias declaration.
func (p *TSParser) extractTypeAlias(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	doc := extractPrecedingComment(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     "type",
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: isExportedTS(name),
		ASTHash:  hashString(node.Content(content)),
	}
}

// extractEnum extracts an enum declaration.
func (p *TSParser) extractEnum(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	doc := extractPrecedingComment(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     "enum",
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: isExportedTS(name),
		ASTHash:  hashString(node.Content(content)),
	}
}

// extractArrowFunctions extracts arrow functions and function expressions from lexical declarations.
// e.g., `const foo = () => {}` or `const foo = function() {}`
func (p *TSParser) extractArrowFunctions(node *sitter.Node, content []byte, filePath string) []FunctionInfo {
	var fns []FunctionInfo

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() != "variable_declarator" {
			continue
		}

		nameNode := child.ChildByFieldName("name")
		valueNode := child.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil {
			continue
		}

		valueType := valueNode.Type()
		if valueType != "arrow_function" && valueType != "function_expression" && valueType != "function" {
			continue
		}

		name := nameNode.Content(content)
		line := int(node.StartPoint().Row) + 1
		sig := extractSignature(node, content)
		doc := extractPrecedingComment(node, content)

		fns = append(fns, FunctionInfo{
			Name:      name,
			Signature: strings.TrimSpace(sig),
			Doc:       doc,
			File:      filePath,
			Line:      line,
			Exported:  isExportedTS(name),
			ASTHash:   hashString(node.Content(content)),
			SigHash:   hashString(sig),
		})
	}

	return fns
}

// extractSignature gets a concise signature from a declaration node.
// Takes the first line (up to the opening brace) as the signature.
func extractSignature(node *sitter.Node, content []byte) string {
	text := node.Content(content)
	// Take everything up to the first { as the signature.
	if idx := strings.Index(text, "{"); idx > 0 {
		text = text[:idx]
	}
	// Collapse to single line.
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	// Truncate very long signatures.
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	return text
}

// extractPrecedingComment looks for a JSDoc or line comment immediately before a node.
func extractPrecedingComment(node *sitter.Node, content []byte) string {
	prev := node.PrevSibling()
	if prev == nil {
		return ""
	}

	if prev.Type() != "comment" {
		return ""
	}

	// Only if the comment is on the line immediately before the declaration.
	if int(node.StartPoint().Row)-int(prev.EndPoint().Row) > 1 {
		return ""
	}

	text := prev.Content(content)
	return cleanComment(text)
}

// cleanComment strips comment markers from JSDoc/line comments.
func cleanComment(text string) string {
	// Strip JSDoc markers.
	text = strings.TrimPrefix(text, "/**")
	text = strings.TrimSuffix(text, "*/")
	text = strings.TrimPrefix(text, "//")

	// Clean up each line.
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		// Skip JSDoc tags.
		if strings.HasPrefix(line, "@") {
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, " ")
}

// isExportedTS checks if a name looks like it's meant to be public.
// In TS, exports are explicit (via `export` keyword), but we also consider
// PascalCase names as likely intended to be public.
func isExportedTS(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

// vueScriptRe matches <script> or <script setup> or <script lang="ts"> blocks.
var vueScriptRe = regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)

// extractVueScript extracts the content of the <script> block from a Vue SFC.
func extractVueScript(content []byte) []byte {
	matches := vueScriptRe.FindSubmatch(content)
	if len(matches) < 2 {
		return nil
	}
	return matches[1]
}
