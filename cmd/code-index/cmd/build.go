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
	RootCmd.AddCommand(buildCmd)
}

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the searchable index from parsed data and cached docs",
	Long: `Combines the parsed AST data with cached LLM-generated summaries
to produce a searchable index.json file. This index is used by the
MCP code_search tool.`,
	RunE: runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
	out, err := absOutputDir()
	if err != nil {
		return err
	}

	// Load the parse result.
	parsedPath := filepath.Join(out, "parsed.json")
	data, err := os.ReadFile(parsedPath)
	if err != nil {
		return fmt.Errorf("reading parse result (run 'code-index parse' first): %w", err)
	}

	var parsed indexer.ParseResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parsing parse result: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Building search index...\n")

	index, err := indexer.BuildIndex(&parsed, out)
	if err != nil {
		return fmt.Errorf("building index: %w", err)
	}

	if err := indexer.WriteIndex(index, out); err != nil {
		return fmt.Errorf("writing index: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n%s", indexer.PrintStats(index))
	fmt.Fprintf(os.Stderr, "\nIndex written to %s\n", filepath.Join(out, "index.json"))

	return nil
}
