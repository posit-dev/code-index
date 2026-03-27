// Copyright (C) 2026 by Posit Software, PBC
package cmd

import (
	"errors"
	"path/filepath"

	"github.com/posit-dev/code-index/indexer"

	"github.com/spf13/cobra"
)

var (
	// srcDir is the Go source directory to index.
	srcDir string
	// outputDir is where the index output is written.
	outputDir string
)

var RootCmd = &cobra.Command{
	Use:   "code-index",
	Short: "Build a searchable code index with LLM-powered summaries",
	Long: `code-index parses source trees using language-specific parsers
(Go, TypeScript/Vue, Python, R, C/C++, Markdown/Quarto), generates
LLM-powered summaries at function, file, and package levels, and
builds a searchable vector index for use with AI coding assistants via MCP.

Configuration is read from .code-index.json in the repository root.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.New("this command requires a sub-command: parse, generate, build, embed, or search. Use --help for details")
	},
}

func init() {
	RootCmd.PersistentFlags().StringVar(&srcDir, "src", "src", "Source directory to index")
	RootCmd.PersistentFlags().StringVar(&outputDir, "output", ".code-index", "Output directory for index data")
}

// absSrcDir returns the absolute path to the source directory.
func absSrcDir() (string, error) {
	return filepath.Abs(srcDir)
}

// absOutputDir returns the absolute path to the output directory.
func absOutputDir() (string, error) {
	return filepath.Abs(outputDir)
}

// repoRoot returns the absolute path to the repository root (parent of the output dir).
func repoRoot() (string, error) {
	out, err := absOutputDir()
	if err != nil {
		return "", err
	}
	return filepath.Dir(out), nil
}

// loadConfig loads the .code-index.json from the repo root.
func loadConfig(root ...string) (*indexer.IndexConfig, error) {
	var r string
	if len(root) > 0 {
		r = root[0]
	} else {
		var err error
		r, err = repoRoot()
		if err != nil {
			return nil, err
		}
	}
	return indexer.LoadConfig(r)
}
