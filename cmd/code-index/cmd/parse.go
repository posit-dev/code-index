// Copyright (C) 2026 by Posit Software, PBC
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/posit-dev/code-index/indexer"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(parseCmd)
}

var parseCmd = &cobra.Command{
	Use:   "parse",
	Short: "Parse source files and extract AST information",
	Long: `Walks the source tree(s) defined in .code-index.json (or defaults),
parses each file using language-appropriate parsers (go/ast for Go,
tree-sitter for TypeScript/Vue), and extracts function signatures,
type definitions, doc comments, and structural hashes.
Writes the parse result to .code-index/parsed.json.`,
	RunE: runParse,
}

func runParse(cmd *cobra.Command, args []string) error {
	src, err := absSrcDir()
	if err != nil {
		return err
	}
	out, err := absOutputDir()
	if err != nil {
		return err
	}

	root := filepath.Dir(src)

	// Load config.
	config, err := loadConfig(root)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	result := indexer.NewParseResult()

	for _, source := range config.Sources {
		srcPath := filepath.Join(root, source.Path)
		fmt.Fprintf(os.Stderr, "Parsing %s (%s)...\n", source.Path, langName(source.Language))

		switch source.Language {
		case "go", "":
			modulePrefix := source.ImportPrefix
			if modulePrefix == "" {
				mp, err := detectModulePrefix(srcPath)
				if err != nil {
					// For vendor sources, use the path as prefix.
					mp = source.Path
				}
				modulePrefix = mp + "/" + source.Path
			}

			parser := indexer.NewParserWithConfig(srcPath, modulePrefix, source.Exclude, source.VendorInclude)
			if err := parser.ParseInto(result); err != nil {
				return fmt.Errorf("parsing Go source %s: %w", source.Path, err)
			}

		case "typescript", "javascript":
			parser := indexer.NewTSParser(srcPath, source.Exclude)
			if err := parser.Parse(result); err != nil {
				return fmt.Errorf("parsing TS source %s: %w", source.Path, err)
			}

		case "python":
			parser := indexer.NewPythonParser(srcPath, source.Exclude)
			if err := parser.Parse(result); err != nil {
				return fmt.Errorf("parsing Python source %s: %w", source.Path, err)
			}

		case "r":
			parser := indexer.NewRParserWithConfig(srcPath, source.Exclude, config.R.Executable, root)
			if err := parser.Parse(result); err != nil {
				return fmt.Errorf("parsing R source %s: %w", source.Path, err)
			}

		case "c":
			parser := indexer.NewCParser(srcPath, source.Exclude)
			if err := parser.Parse(result); err != nil {
				return fmt.Errorf("parsing C source %s: %w", source.Path, err)
			}

		case "cpp":
			parser := indexer.NewCPPParser(srcPath, source.Exclude)
			if err := parser.Parse(result); err != nil {
				return fmt.Errorf("parsing C++ source %s: %w", source.Path, err)
			}

		case "markdown":
			parser := indexer.NewMarkdownParser(srcPath, source.Exclude)
			if err := parser.Parse(result); err != nil {
				return fmt.Errorf("parsing Markdown source %s: %w", source.Path, err)
			}
		}
	}

	// Write parsed result.
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling parse result: %w", err)
	}

	parsedPath := filepath.Join(out, "parsed.json")
	if err := os.WriteFile(parsedPath, data, 0o644); err != nil {
		return fmt.Errorf("writing parse result: %w", err)
	}

	// Print summary.
	fmt.Fprintf(os.Stderr, "\nParsed %d packages, %d files\n", len(result.Packages), len(result.Files))
	var totalFuncs, totalTypes int
	for _, f := range result.Files {
		totalFuncs += len(f.Functions)
		totalTypes += len(f.Types)
	}
	fmt.Fprintf(os.Stderr, "Found %d functions, %d types\n", totalFuncs, totalTypes)
	fmt.Fprintf(os.Stderr, "Written to %s\n", parsedPath)

	return nil
}

func langName(lang string) string {
	switch lang {
	case "go", "":
		return "Go"
	case "typescript":
		return "TypeScript"
	case "javascript":
		return "JavaScript"
	case "python":
		return "Python"
	case "c":
		return "C"
	case "cpp":
		return "C++"
	case "r":
		return "R"
	case "markdown":
		return "Markdown"
	default:
		return lang
	}
}

// detectModulePrefix reads the module name from go.mod.
func detectModulePrefix(srcDir string) (string, error) {
	// go.mod should be in the parent of the src directory.
	goModPath := filepath.Join(filepath.Dir(srcDir), "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("reading go.mod at %s: %w", goModPath, err)
	}

	for _, line := range splitLines(string(data)) {
		if len(line) > 7 && line[:7] == "module " {
			return line[7:], nil
		}
	}

	return "", fmt.Errorf("module directive not found in %s", goModPath)
}

func splitLines(s string) []string {
	var lines []string
	for len(s) > 0 {
		i := 0
		for i < len(s) && s[i] != '\n' {
			i++
		}
		line := s[:i]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		lines = append(lines, line)
		if i < len(s) {
			i++
		}
		s = s[i:]
	}
	return lines
}
