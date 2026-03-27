// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkdownParser(t *testing.T) {
	// Create a temp directory with a test markdown file.
	dir := t.TempDir()
	mdContent := `---
title: Test Document
description: A test document for the markdown parser.
---

# Introduction

This is the introduction section.

## Getting Started

Follow these steps to get started:

1. Install the tool
2. Configure it
3. Run it

### Prerequisites

You need Go 1.21 or later.

## API Reference

The API provides the following endpoints.

` + "```go\n// This code block should be stripped from doc content.\nfunc main() {}\n```" + `

More content after the code block.
`

	err := os.WriteFile(filepath.Join(dir, "test.md"), []byte(mdContent), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	parser := NewMarkdownParser(dir, nil)
	result := NewParseResult()

	err = parser.Parse(result)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Should have parsed the file.
	if len(result.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(result.Files))
	}

	var fileInfo *FileInfo
	for _, f := range result.Files {
		fileInfo = f
		break
	}

	// File doc should come from front matter.
	if fileInfo.Doc != "A test document for the markdown parser." {
		t.Errorf("file doc = %q, want front matter description", fileInfo.Doc)
	}

	// Should have sections.
	if len(fileInfo.Functions) < 4 {
		t.Fatalf("sections = %d, want at least 4", len(fileInfo.Functions))
	}

	// Check first section.
	found := false
	for _, fn := range fileInfo.Functions {
		if fn.Name == "Introduction" {
			found = true
			if fn.Signature != "# Introduction" {
				t.Errorf("signature = %q, want %q", fn.Signature, "# Introduction")
			}
			if fn.Line != 6 {
				t.Errorf("line = %d, want 6", fn.Line)
			}
		}
		if fn.Name == "API Reference" {
			// Doc should not contain the code block.
			if contains(fn.Doc, "func main()") {
				t.Errorf("API Reference doc contains code block content: %q", fn.Doc)
			}
		}
	}
	if !found {
		t.Error("Introduction section not found")
	}
}

func TestMarkdownParserQuarto(t *testing.T) {
	dir := t.TempDir()
	qmdContent := `---
title: Quarto Doc
---

## First Section

Content here.

## Second Section

More content.
`
	err := os.WriteFile(filepath.Join(dir, "test.qmd"), []byte(qmdContent), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	parser := NewMarkdownParser(dir, nil)
	result := NewParseResult()

	err = parser.Parse(result)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(result.Files))
	}

	var fileInfo *FileInfo
	for _, f := range result.Files {
		fileInfo = f
		break
	}

	if len(fileInfo.Functions) != 2 {
		t.Errorf("sections = %d, want 2", len(fileInfo.Functions))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
