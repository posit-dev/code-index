// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// RParser extracts structured information from R source files.
// It uses R's native parser (via Rscript) for accurate parsing when available,
// falling back to regex-based extraction otherwise.
type RParser struct {
	srcRoot    string
	excludes   []string
	repoRoot   string
	rscriptBin string
	useNative  bool
}

// rscriptOnce ensures we only check for Rscript availability once.
var rscriptOnce sync.Once

// rscriptAvailable caches whether Rscript was found.
var rscriptAvailable bool

// rscriptPath caches the resolved Rscript path.
var rscriptPath string

// NewRParser creates a new R parser.
func NewRParser(srcRoot string, excludes []string) *RParser {
	return NewRParserWithConfig(srcRoot, excludes, "", "")
}

// NewRParserWithConfig creates a new R parser with an explicit Rscript path and repo root.
// If rscriptBin is empty, Rscript is looked up in PATH.
// If repoRoot is empty, it defaults to the parent of srcRoot.
func NewRParserWithConfig(srcRoot string, excludes []string, rscriptBin, repoRoot string) *RParser {
	if repoRoot == "" {
		repoRoot = filepath.Dir(srcRoot)
	}
	p := &RParser{
		srcRoot:  srcRoot,
		excludes: excludes,
		repoRoot: repoRoot,
	}

	rscriptOnce.Do(func() {
		bin := rscriptBin
		if bin == "" {
			var err error
			bin, err = exec.LookPath("Rscript")
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: Rscript not found in PATH, using regex fallback for R parsing.\n")
				fmt.Fprintf(os.Stderr, "  For better R parsing, install R: https://cloud.r-project.org/ or `brew install r` (macOS)\n")
				rscriptAvailable = false
				return
			}
		}
		// Verify the binary actually works.
		cmd := exec.Command(bin, "--version")
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: Rscript at %s is not functional (%v), using regex fallback\n", bin, err)
			rscriptAvailable = false
			return
		}
		rscriptPath = bin
		rscriptAvailable = true
	})

	p.rscriptBin = rscriptPath
	p.useNative = rscriptAvailable
	return p
}

// rNativeOutput is the JSON structure returned by the R parse script.
type rNativeOutput struct {
	Functions []rNativeFunction `json:"functions"`
	Types     []rNativeType     `json:"types"`
}

type rNativeFunction struct {
	Name      string `json:"name"`
	Signature string `json:"signature"`
	Doc       string `json:"doc"`
	Line      int    `json:"line"`
	Exported  bool   `json:"exported"`
}

type rNativeType struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Doc  string `json:"doc"`
	Line int    `json:"line"`
}

// Patterns for R code extraction (regex fallback).
var (
	// Matches: func_name <- function(args) or func_name = function(args)
	rFuncDef = regexp.MustCompile(`(?m)^(\w[\w.]*)\s*(?:<-|=)\s*function\s*\(([^)]*)\)`)
	// Matches S4 class: setClass("ClassName", ...)
	rSetClass = regexp.MustCompile(`(?m)setClass\s*\(\s*"(\w+)"`)
	// Matches R6 class: ClassName <- R6Class("ClassName", ...)
	rR6Class = regexp.MustCompile(`(?m)^(\w+)\s*<-\s*R6::?R6Class\s*\(`)
)

// Parse walks the source tree and extracts all file, function, and type information.
func (p *RParser) Parse(result *ParseResult) error {
	return filepath.Walk(p.srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".r" {
			return nil
		}

		relPath, err := filepath.Rel(p.srcRoot, path)
		if err != nil {
			return nil
		}

		for _, pattern := range p.excludes {
			if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
				return nil
			}
			if matched, err := filepath.Match(pattern, filepath.Base(path)); err == nil && matched {
				return nil
			}
		}

		if p.useNative {
			return p.parseFileNative(path, relPath, result)
		}
		return p.parseFileRegex(path, relPath, result)
	})
}

// parseFileNative parses a single R file using Rscript.
func (p *RParser) parseFileNative(path, relPath string, result *ParseResult) error {
	scriptPath := filepath.Join(p.repoRoot, "scripts", "parse-r.R")
	cmd := exec.Command(p.rscriptBin, "--vanilla", scriptPath, path)
	output, err := cmd.Output()
	if err != nil {
		// Fall back to regex on any error.
		fmt.Fprintf(os.Stderr, "warning: native R parse failed for %s: %v, falling back to regex\n", relPath, err)
		return p.parseFileRegex(path, relPath, result)
	}

	var native rNativeOutput
	if err := json.Unmarshal(output, &native); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to parse R script output for %s: %v, falling back to regex\n", relPath, err)
		return p.parseFileRegex(path, relPath, result)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(content)

	dir := filepath.Dir(relPath)
	importPath := filepath.ToSlash(dir)

	fileInfo := &FileInfo{
		Path:       filepath.ToSlash(relPath),
		Package:    filepath.Base(dir),
		ImportPath: importPath,
		ASTHash:    hashString(text),
	}

	for _, f := range native.Functions {
		sig := f.Signature
		// Extract just the function(...) part for hashing.
		sigPart := sig
		if idx := strings.Index(sig, "function("); idx >= 0 {
			sigPart = sig[idx:]
		}

		fileInfo.Functions = append(fileInfo.Functions, FunctionInfo{
			Name:      f.Name,
			Signature: sig,
			Doc:       f.Doc,
			File:      fileInfo.Path,
			Line:      f.Line,
			Exported:  f.Exported,
			ASTHash:   hashString(f.Name + sigPart),
			SigHash:   hashString(sigPart),
		})
	}

	for _, t := range native.Types {
		fileInfo.Types = append(fileInfo.Types, TypeInfo{
			Name:     t.Name,
			Kind:     t.Kind,
			Doc:      t.Doc,
			File:     fileInfo.Path,
			Line:     t.Line,
			Exported: true,
			ASTHash:  hashString(t.Kind[:2] + ":" + t.Name),
		})
	}

	if len(fileInfo.Functions) > 0 || len(fileInfo.Types) > 0 {
		result.Files[fileInfo.Path] = fileInfo

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

// parseFileRegex parses a single R file using regex patterns (fallback).
func (p *RParser) parseFileRegex(path, relPath string, result *ParseResult) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	text := string(content)
	lines := strings.Split(text, "\n")

	dir := filepath.Dir(relPath)
	importPath := filepath.ToSlash(dir)

	fileInfo := &FileInfo{
		Path:       filepath.ToSlash(relPath),
		Package:    filepath.Base(dir),
		ImportPath: importPath,
		ASTHash:    hashString(text),
	}

	// Extract functions with their preceding roxygen comments.
	funcMatches := rFuncDef.FindAllStringSubmatchIndex(text, -1)
	for _, match := range funcMatches {
		name := text[match[2]:match[3]]
		params := text[match[4]:match[5]]
		line := strings.Count(text[:match[0]], "\n") + 1

		// Look for roxygen comments above the function.
		doc := extractRoxygenDoc(lines, line-1)

		sig := "function(" + strings.TrimSpace(params) + ")"
		if len(sig) > 200 {
			sig = sig[:200] + "..."
		}

		fileInfo.Functions = append(fileInfo.Functions, FunctionInfo{
			Name:      name,
			Signature: name + " <- " + sig,
			Doc:       doc,
			File:      fileInfo.Path,
			Line:      line,
			Exported:  !strings.HasPrefix(name, "."),
			ASTHash:   hashString(name + sig),
			SigHash:   hashString(sig),
		})
	}

	// Extract S4 classes.
	s4Matches := rSetClass.FindAllStringSubmatchIndex(text, -1)
	for _, match := range s4Matches {
		name := text[match[2]:match[3]]
		line := strings.Count(text[:match[0]], "\n") + 1
		fileInfo.Types = append(fileInfo.Types, TypeInfo{
			Name:     name,
			Kind:     "S4 class",
			File:     fileInfo.Path,
			Line:     line,
			Exported: true,
			ASTHash:  hashString("S4:" + name),
		})
	}

	// Extract R6 classes.
	r6Matches := rR6Class.FindAllStringSubmatchIndex(text, -1)
	for _, match := range r6Matches {
		name := text[match[2]:match[3]]
		line := strings.Count(text[:match[0]], "\n") + 1
		fileInfo.Types = append(fileInfo.Types, TypeInfo{
			Name:     name,
			Kind:     "R6 class",
			File:     fileInfo.Path,
			Line:     line,
			Exported: true,
			ASTHash:  hashString("R6:" + name),
		})
	}

	if len(fileInfo.Functions) > 0 || len(fileInfo.Types) > 0 {
		result.Files[fileInfo.Path] = fileInfo

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

// extractRoxygenDoc collects roxygen comment lines (#') immediately above a function definition.
func extractRoxygenDoc(lines []string, funcLineIdx int) string {
	var roxyLines []string
	for i := funcLineIdx - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "#'") {
			text := strings.TrimPrefix(line, "#'")
			text = strings.TrimSpace(text)
			// Skip roxygen tags except @title and @description.
			if strings.HasPrefix(text, "@") && !strings.HasPrefix(text, "@title") && !strings.HasPrefix(text, "@description") {
				continue
			}
			text = strings.TrimPrefix(text, "@title ")
			text = strings.TrimPrefix(text, "@description ")
			if text != "" {
				roxyLines = append([]string{text}, roxyLines...)
			}
		} else {
			break
		}
	}
	result := strings.Join(roxyLines, " ")
	if len(result) > 300 {
		result = result[:300] + "..."
	}
	return result
}
