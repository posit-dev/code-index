// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SearchIndex is the searchable index structure written for the MCP tool.
type SearchIndex struct {
	// Packages with their summaries.
	Packages []PackageEntry `json:"packages"`
	// Files with their summaries.
	Files []FileEntry `json:"files"`
	// Functions with their summaries and signatures.
	Functions []FunctionEntry `json:"functions"`
	// Types with their summaries.
	Types []TypeEntry `json:"types"`
}

// PackageEntry is a package in the search index.
type PackageEntry struct {
	ImportPath string `json:"import_path"`
	Dir        string `json:"dir"`
	Doc        string `json:"doc,omitempty"`
	Summary    string `json:"summary,omitempty"`
	FileCount  int    `json:"file_count"`
}

// FileEntry is a file in the search index.
type FileEntry struct {
	Path       string `json:"path"`
	Package    string `json:"package"`
	ImportPath string `json:"import_path"`
	Doc        string `json:"doc,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// FunctionEntry is a function in the search index.
type FunctionEntry struct {
	Name      string `json:"name"`
	Receiver  string `json:"receiver,omitempty"`
	Signature string `json:"signature"`
	Doc       string `json:"doc,omitempty"`
	Summary   string `json:"summary,omitempty"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Exported  bool   `json:"exported"`
}

// TypeEntry is a type in the search index.
type TypeEntry struct {
	Name     string      `json:"name"`
	Kind     string      `json:"kind"`
	Doc      string      `json:"doc,omitempty"`
	Summary  string      `json:"summary,omitempty"`
	File     string      `json:"file"`
	Line     int         `json:"line"`
	Exported bool        `json:"exported"`
	Fields   []FieldInfo `json:"fields,omitempty"`
}

// BuildIndex constructs the searchable index from parsed results and cached docs.
func BuildIndex(parsed *ParseResult, outputDir string) (*SearchIndex, error) {
	docsDir := filepath.Join(outputDir, CacheDir)
	index := &SearchIndex{}

	// Build package entries.
	for importPath, pkg := range parsed.Packages {
		summary := readDocFile(filepath.Join(docsDir, "pkg"), importPath)
		index.Packages = append(index.Packages, PackageEntry{
			ImportPath: importPath,
			Dir:        pkg.Dir,
			Doc:        pkg.Doc,
			Summary:    summary,
			FileCount:  len(pkg.Files),
		})
	}

	// Build file and function entries.
	for filePath, fileInfo := range parsed.Files {
		fileSummary := readDocFileByPath(fileDocPath(docsDir, filePath))
		index.Files = append(index.Files, FileEntry{
			Path:       filePath,
			Package:    fileInfo.Package,
			ImportPath: fileInfo.ImportPath,
			Doc:        fileInfo.Doc,
			Summary:    fileSummary,
		})

		for _, fn := range fileInfo.Functions {
			key := FunctionCacheKey(fn.File, fn.Name, fn.Receiver)
			funcSummary := readDocFile(filepath.Join(docsDir, "func"), key)
			index.Functions = append(index.Functions, FunctionEntry{
				Name:      fn.Name,
				Receiver:  fn.Receiver,
				Signature: fn.Signature,
				Doc:       fn.Doc,
				Summary:   funcSummary,
				File:      fn.File,
				Line:      fn.Line,
				Exported:  fn.Exported,
			})
		}

		for _, t := range fileInfo.Types {
			index.Types = append(index.Types, TypeEntry{
				Name:     t.Name,
				Kind:     t.Kind,
				Doc:      t.Doc,
				File:     t.File,
				Line:     t.Line,
				Exported: t.Exported,
				Fields:   t.Fields,
			})
		}
	}

	return index, nil
}

// WriteIndex writes the search index to a JSON file.
func WriteIndex(index *SearchIndex, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling index: %w", err)
	}

	path := filepath.Join(outputDir, "index.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing index: %w", err)
	}

	return nil
}

// PrintStats prints summary statistics for the index.
func PrintStats(index *SearchIndex) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Packages:  %d\n", len(index.Packages))
	fmt.Fprintf(&sb, "Files:     %d\n", len(index.Files))
	fmt.Fprintf(&sb, "Functions: %d\n", len(index.Functions))
	fmt.Fprintf(&sb, "Types:     %d\n", len(index.Types))

	// Count items with summaries.
	var pkgWithSummary, fileWithSummary, funcWithSummary int
	for _, p := range index.Packages {
		if p.Summary != "" {
			pkgWithSummary++
		}
	}
	for _, f := range index.Files {
		if f.Summary != "" {
			fileWithSummary++
		}
	}
	for _, f := range index.Functions {
		if f.Summary != "" {
			funcWithSummary++
		}
	}

	sb.WriteString("\nWith LLM summaries:\n")
	fmt.Fprintf(&sb, "  Packages:  %d/%d\n", pkgWithSummary, len(index.Packages))
	fmt.Fprintf(&sb, "  Files:     %d/%d\n", fileWithSummary, len(index.Files))
	fmt.Fprintf(&sb, "  Functions: %d/%d\n", funcWithSummary, len(index.Functions))

	return sb.String()
}
