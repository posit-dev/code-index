// Copyright (C) 2026 by Posit Software, PBC
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func init() {
	allCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Check what would be generated without making LLM calls")
	allCmd.Flags().IntVar(&maxFiles, "limit", 0, "Limit number of items to process (0 = unlimited)")
	allCmd.Flags().StringVar(&backend, "backend", "", `LLM backend override: "bedrock", "cli", or "" (use config)`)
	allCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Verbose logging of LLM calls")
	allCmd.Flags().BoolVar(&resetVectors, "reset", false, "Delete and rebuild the vector database")
	RootCmd.AddCommand(allCmd)
}

var allCmd = &cobra.Command{
	Use:   "all",
	Short: "Run parse, generate, build, and embed in sequence",
	Long:  `Convenience command that runs all four steps: parse, generate, build, embed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(os.Stderr, "=== Step 1/4: Parse ===\n")
		if err := runParse(cmd, args); err != nil {
			return fmt.Errorf("parse: %w", err)
		}

		fmt.Fprintf(os.Stderr, "\n=== Step 2/4: Generate ===\n")
		if err := runGenerate(cmd, args); err != nil {
			return fmt.Errorf("generate: %w", err)
		}

		fmt.Fprintf(os.Stderr, "\n=== Step 3/4: Build ===\n")
		if err := runBuild(cmd, args); err != nil {
			return fmt.Errorf("build: %w", err)
		}

		fmt.Fprintf(os.Stderr, "\n=== Step 4/4: Embed ===\n")
		if err := runEmbed(cmd, args); err != nil {
			return fmt.Errorf("embed: %w", err)
		}

		return nil
	},
}
