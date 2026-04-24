// Copyright (C) 2026 by Posit Software, PBC
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/posit-dev/code-index/indexer"

	"github.com/spf13/cobra"
)

var (
	searchMaxResults int
	searchJSON       bool
)

func init() {
	searchCmd.Flags().IntVarP(&searchMaxResults, "max-results", "n", 10, "Maximum number of results")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Output results as JSON")
	RootCmd.AddCommand(searchCmd)
}

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search the vector database",
	Long:  `Searches the vector database for functions, types, files, and packages matching the query.`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSearch,
}

func runSearch(cmd *cobra.Command, args []string) error {
	out, err := absOutputDir()
	if err != nil {
		return err
	}

	query := strings.Join(args, " ")

	// Load config.
	config, err := loadConfig()
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Create embedder for the query.
	embedder, err := indexer.NewEmbedder(ctx, config.Embeddings, config.AWS.Region)
	if err != nil {
		return fmt.Errorf("creating embedder: %w", err)
	}

	// Embed the query.
	queryEmbedding, err := embedder.EmbedQuery(ctx, query)
	if err != nil {
		return fmt.Errorf("embedding query: %w", err)
	}

	// Open the vector store (dimensions=0 reads stored value from DB).
	store, err := indexer.OpenVectorStore(out, 0)
	if err != nil {
		return fmt.Errorf("opening vector store: %w", err)
	}
	defer store.Close() //nolint:errcheck

	// Search using hybrid BM25 + vector.
	alpha := *config.Search.Alpha
	results, err := store.HybridSearch(
		ctx, queryEmbedding, query, searchMaxResults, alpha)
	if err != nil {
		return fmt.Errorf("searching: %w", err)
	}

	if len(results) == 0 {
		if searchJSON {
			fmt.Println("[]")
		} else {
			fmt.Println("No results found.")
		}
		return nil
	}

	if searchJSON {
		type jsonResult struct {
			Rank       int               `json:"rank"`
			Score      float32           `json:"score"`
			Similarity float32           `json:"similarity"`
			Metadata   map[string]string `json:"metadata"`
		}
		var jsonResults []jsonResult
		for i, r := range results {
			jsonResults = append(jsonResults, jsonResult{
				Rank:       i + 1,
				Score:      r.Score,
				Similarity: r.Similarity,
				Metadata:   r.Metadata,
			})
		}
		data, err := json.MarshalIndent(jsonResults, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling results: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Fprintf(os.Stderr, "Found %d results for %q:\n\n", len(results), query)

	for i, r := range results {
		kind := r.Metadata["kind"]
		name := r.Metadata["name"]
		file := r.Metadata["file"]
		line := r.Metadata["line"]
		sig := r.Metadata["signature"]
		summary := r.Metadata["summary"]
		doc := r.Metadata["doc"]

		score := r.Similarity
		if alpha < 1.0 {
			score = r.Score
		}
		label := "match"
		if alpha < 1.0 {
			label = "relevance"
		}
		fmt.Printf("%d. [%s] %s (%.1f%% %s)\n",
			i+1, kind, name, score*100, label)

		if sig != "" {
			fmt.Printf("   %s\n", sig)
		}
		if file != "" && line != "" && line != "0" {
			fmt.Printf("   %s:%s\n", file, line)
		} else if file != "" {
			fmt.Printf("   %s\n", file)
		}
		if summary != "" {
			fmt.Printf("   %s\n", summary)
		} else if doc != "" {
			if len(doc) > 120 {
				doc = doc[:120] + "..."
			}
			fmt.Printf("   %s\n", doc)
		}
		fmt.Println()
	}

	return nil
}
