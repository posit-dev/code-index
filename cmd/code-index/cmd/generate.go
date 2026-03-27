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

var (
	dryRun   bool
	maxFiles int
	backend  string
	verbose  bool
)

func init() {
	generateCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Check what would be generated without making LLM calls")
	generateCmd.Flags().IntVar(&maxFiles, "limit", 0, "Limit number of files to process (0 = unlimited)")
	generateCmd.Flags().StringVar(&backend, "backend", "", `LLM backend override: "bedrock", "openai", or "" (use config)`)
	generateCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose logging of LLM calls")
	RootCmd.AddCommand(generateCmd)
}

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate LLM summaries for changed code",
	Long: `Compares the current parse result against the cache manifest to find
changed functions, files, and packages. Generates LLM-powered summaries
for items that have changed.

The LLM provider and model IDs are configured in .code-index.json.
Use --backend to override the provider.`,
	RunE: runGenerate,
}

func runGenerate(cmd *cobra.Command, args []string) error {
	out, err := absOutputDir()
	if err != nil {
		return err
	}

	// Load config.
	config, err := loadConfig()
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

	// Load the cache manifest.
	cache, err := indexer.LoadCacheManifest(out)
	if err != nil {
		return fmt.Errorf("loading cache: %w", err)
	}

	// Compute what needs regeneration.
	diff := indexer.ComputeDiff(&parsed, cache)

	fmt.Fprintf(os.Stderr, "Changes detected:\n")
	fmt.Fprintf(os.Stderr, "  Functions: %d changed, %d removed\n", len(diff.ChangedFunctions), len(diff.RemovedFunctions))
	fmt.Fprintf(os.Stderr, "  Files:     %d changed, %d removed\n", len(diff.ChangedFiles), len(diff.RemovedFiles))
	fmt.Fprintf(os.Stderr, "  Packages:  %d changed, %d removed\n", len(diff.ChangedPackages), len(diff.RemovedPackages))

	if len(diff.ChangedFunctions) == 0 && len(diff.ChangedFiles) == 0 && len(diff.ChangedPackages) == 0 &&
		len(diff.RemovedFunctions) == 0 && len(diff.RemovedFiles) == 0 && len(diff.RemovedPackages) == 0 {
		fmt.Fprintf(os.Stderr, "\nNo changes detected. Index is up to date.\n")
		return nil
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "\n--dry-run: would generate docs for the above changes.\n")
		return nil
	}

	// Generate docs.
	var opts []indexer.GeneratorOption
	if maxFiles > 0 {
		opts = append(opts, indexer.WithMaxFiles(maxFiles))
	}
	if verbose {
		opts = append(opts, indexer.WithVerbose(true))
	}
	if backend != "" {
		opts = append(opts, indexer.WithBackendOverride(backend))
	}
	generator, err := indexer.NewGenerator(out, config, false, opts...)
	if err != nil {
		return err
	}

	stats, err := generator.Generate(&parsed, diff, cache)
	if err != nil {
		return fmt.Errorf("generating docs: %w", err)
	}

	// Save updated cache.
	if err := indexer.SaveCacheManifest(out, cache); err != nil {
		return fmt.Errorf("saving cache: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nGeneration complete:\n")
	fmt.Fprintf(os.Stderr, "  Functions: %d generated\n", stats.FunctionsGenerated)
	fmt.Fprintf(os.Stderr, "  Files:     %d generated\n", stats.FilesGenerated)
	fmt.Fprintf(os.Stderr, "  Packages:  %d generated\n", stats.PackagesGenerated)
	if stats.FunctionsRemoved+stats.FilesRemoved+stats.PackagesRemoved > 0 {
		fmt.Fprintf(os.Stderr, "  Removed:   %d functions, %d files, %d packages\n",
			stats.FunctionsRemoved, stats.FilesRemoved, stats.PackagesRemoved)
	}

	return nil
}
