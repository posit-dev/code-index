// Copyright (C) 2026 by Posit Software, PBC
package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/posit-dev/code-index/indexer"

	"github.com/spf13/cobra"
)

var resetVectors bool

func init() {
	embedCmd.Flags().IntVar(&maxFiles, "limit", 0, "Limit number of items to embed (0 = unlimited)")
	embedCmd.Flags().BoolVar(&resetVectors, "reset", false, "Delete and rebuild the entire vector database")
	RootCmd.AddCommand(embedCmd)
}

var embedCmd = &cobra.Command{
	Use:   "embed",
	Short: "Generate embeddings and build the vector search database",
	Long: `Reads the search index (index.json) and generates vector embeddings
for all functions, types, files, and packages using the configured
embedding provider. Stores results in a SQLite database with sqlite-vec.

Supports incremental updates — only embeds items not already in the database.
Use --reset to rebuild from scratch.`,
	RunE: runEmbed,
}

func runEmbed(cmd *cobra.Command, args []string) error {
	out, err := absOutputDir()
	if err != nil {
		return err
	}

	// Load the search index.
	indexPath := filepath.Join(out, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("reading index (run 'code-index build' first): %w", err)
	}

	var searchIndex indexer.SearchIndex
	if err := json.Unmarshal(data, &searchIndex); err != nil {
		return fmt.Errorf("parsing index: %w", err)
	}

	// Load config.
	config, err := loadConfig()
	if err != nil {
		return err
	}

	// Create the embedder.
	ctx := context.Background()
	embedder, err := indexer.NewEmbedder(ctx, config.Embeddings, config.AWS.Region)
	if err != nil {
		return fmt.Errorf("creating embedder: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Using embedder: %s\n", embedder.Name())

	// Load the embed cache for incremental updates.
	embedCache, err := indexer.LoadEmbedCache(out)
	if err != nil {
		return fmt.Errorf("loading embed cache: %w", err)
	}

	if resetVectors {
		embedCache = indexer.NewEmbedCache()
	}

	// Build the list of items to embed.
	type embedItem struct {
		id          string
		text        string
		contentHash string
		meta        indexer.DocumentMetadata
	}

	var items []embedItem

	// Functions.
	for _, fn := range searchIndex.Functions {
		if !fn.Exported {
			continue // Only embed exported functions for now.
		}
		id := fmt.Sprintf("func:%s:%s:%d", fn.File, fn.Name, fn.Line)
		text := indexer.BuildEmbeddingText(fn.Name, fn.Signature, fn.Summary, fn.Doc, fn.File)
		items = append(items, embedItem{
			id:          id,
			text:        text,
			contentHash: quickHash(text),
			meta: indexer.DocumentMetadata{
				Kind:      "function",
				Name:      fn.Name,
				Signature: fn.Signature,
				File:      fn.File,
				Line:      fn.Line,
				Receiver:  fn.Receiver,
				Summary:   fn.Summary,
				Doc:       fn.Doc,
			},
		})
	}

	// Types.
	for _, t := range searchIndex.Types {
		if !t.Exported {
			continue
		}
		id := fmt.Sprintf("type:%s:%s:%d", t.File, t.Name, t.Line)
		text := indexer.BuildEmbeddingText(t.Name, t.Kind+" "+t.Name, t.Summary, t.Doc, t.File)
		items = append(items, embedItem{
			id:          id,
			text:        text,
			contentHash: quickHash(text),
			meta: indexer.DocumentMetadata{
				Kind:    "type",
				Name:    t.Name,
				File:    t.File,
				Line:    t.Line,
				Summary: t.Summary,
				Doc:     t.Doc,
			},
		})
	}

	// Files.
	for _, f := range searchIndex.Files {
		id := fmt.Sprintf("file:%s", f.Path)
		text := indexer.BuildEmbeddingText(f.Path, f.Package, f.Summary, f.Doc, f.Path)
		items = append(items, embedItem{
			id:          id,
			text:        text,
			contentHash: quickHash(text),
			meta: indexer.DocumentMetadata{
				Kind:    "file",
				Name:    f.Path,
				File:    f.Path,
				Package: f.ImportPath,
				Summary: f.Summary,
				Doc:     f.Doc,
			},
		})
	}

	// Packages.
	for _, p := range searchIndex.Packages {
		id := fmt.Sprintf("pkg:%s", p.ImportPath)
		text := indexer.BuildEmbeddingText(p.ImportPath, p.Dir, p.Summary, p.Doc, p.Dir)
		items = append(items, embedItem{
			id:          id,
			text:        text,
			contentHash: quickHash(text),
			meta: indexer.DocumentMetadata{
				Kind:    "package",
				Name:    p.ImportPath,
				Package: p.ImportPath,
				Summary: p.Summary,
				Doc:     p.Doc,
			},
		})
	}

	// Filter out items that haven't changed since last embed.
	var needsEmbed []embedItem
	skipped := 0
	for _, item := range items {
		if embedCache.IsUpToDate(item.id, item.contentHash) {
			skipped++
			continue
		}
		needsEmbed = append(needsEmbed, item)
	}

	total := len(needsEmbed)
	if maxFiles > 0 && total > maxFiles {
		total = maxFiles
	}

	// Count items by kind for the summary.
	kindCounts := make(map[string]int)
	for _, item := range needsEmbed {
		kindCounts[item.meta.Kind]++
	}
	fmt.Fprintf(os.Stderr, "\nItems to embed: %d (skipped %d unchanged, %d total)\n", total, skipped, len(items))
	for _, kind := range []string{"function", "type", "file", "package"} {
		if c, ok := kindCounts[kind]; ok {
			fmt.Fprintf(os.Stderr, "  %ss: %d\n", kind, c)
		}
	}

	if total == 0 {
		fmt.Fprintf(os.Stderr, "\nAll embeddings are up to date.\n")
		return nil
	}

	// Detect embedding dimensions from the first item.
	probeText := needsEmbed[0].text
	probeEmb, err := embedder.EmbedDocument(ctx, probeText)
	if err != nil {
		return fmt.Errorf("detecting embedding dimensions: %w", err)
	}
	dims := len(probeEmb)
	fmt.Fprintf(os.Stderr, "Embedding dimensions: %d\n", dims)

	// Open the vector store with detected dimensions.
	store, err := indexer.OpenVectorStore(out, dims)
	if err != nil {
		return fmt.Errorf("opening vector store: %w", err)
	}
	defer store.Close() //nolint:errcheck

	if resetVectors {
		fmt.Fprintf(os.Stderr, "Resetting vector database...\n")
		if err := store.Reset(ctx); err != nil {
			return fmt.Errorf("resetting vector store: %w", err)
		}
	}

	existingCount := store.Count()
	fmt.Fprintf(os.Stderr, "Existing vectors: %d\n", existingCount)

	// Store the probe embedding (first item) so we don't re-embed it.
	firstItem := needsEmbed[0]
	if err := store.AddDocument(ctx, firstItem.id, firstItem.text, probeEmb, firstItem.meta); err != nil {
		return fmt.Errorf("storing probe item %s: %w", firstItem.id, err)
	}
	embedCache.Set(firstItem.id, firstItem.contentHash)

	// Remove the first item from the processing list.
	needsEmbed = needsEmbed[1:]
	total--

	if total == 0 {
		// The probe was the only item. Save cache and exit.
		if err := embedCache.Save(out); err != nil {
			return fmt.Errorf("saving embed cache: %w", err)
		}
		fmt.Fprintf(os.Stderr, "\nDone: 1 embedded, %d skipped\n", skipped)
		fmt.Fprintf(os.Stderr, "Total vectors in database: %d\n", store.Count())
		return nil
	}

	// Process items with parallel workers.
	numWorkers := runtime.NumCPU()
	if numWorkers < 4 {
		numWorkers = 4
	}
	fmt.Fprintf(os.Stderr, "Workers: %d\n", numWorkers)

	type result struct {
		index       int
		id          string
		contentHash string
		embedding   []float32
		meta        indexer.DocumentMetadata
		content     string
		err         error
	}

	// Limit items to process.
	processItems := needsEmbed
	if maxFiles > 0 && len(processItems) > maxFiles {
		processItems = processItems[:maxFiles]
	}

	// Fan out: send items to workers.
	itemCh := make(chan int, len(processItems))
	resultCh := make(chan result, numWorkers*2)

	for w := 0; w < numWorkers; w++ {
		go func() {
			for idx := range itemCh {
				item := processItems[idx]
				emb, err := embedder.EmbedDocument(ctx, item.text)
				resultCh <- result{
					index:       idx,
					id:          item.id,
					contentHash: item.contentHash,
					embedding:   emb,
					meta:        item.meta,
					content:     item.text,
					err:         err,
				}
			}
		}()
	}

	// Send all items to workers.
	go func() {
		for i := range processItems {
			itemCh <- i
		}
		close(itemCh)
	}()

	// Collect results with progress reporting.
	// Start at 1 because we already embedded the probe item.
	embedded := 1
	errors := 0
	var failedItems []embedItem
	startTime := time.Now()
	lastReport := startTime

	for range processItems {
		r := <-resultCh

		now := time.Now()
		if now.Sub(lastReport) >= 2*time.Second || embedded == 0 {
			elapsed := now.Sub(startTime)
			rate := float64(0)
			eta := ""
			if embedded > 0 {
				rate = float64(embedded) / elapsed.Seconds()
				remaining := float64(total-embedded) / rate
				eta = fmt.Sprintf(", ETA %s", (time.Duration(remaining) * time.Second).Round(time.Second))
			}
			fmt.Fprintf(os.Stderr, "  [%d/%d] %.1f items/sec%s\n", embedded+1, total, rate, eta)
			lastReport = now
		}

		if r.err != nil {
			fmt.Fprintf(os.Stderr, "  warning: failed to embed %s: %v\n", r.id, r.err)
			// Track for retry.
			failedItems = append(failedItems, processItems[r.index])
			errors++
			continue
		}

		if err := store.AddDocument(ctx, r.id, r.content, r.embedding, r.meta); err != nil {
			return fmt.Errorf("storing %s: %w", r.id, err)
		}
		embedCache.Set(r.id, r.contentHash)
		embedded++
	}

	// Retry failed items sequentially (transient errors often resolve on retry).
	if len(failedItems) > 0 {
		fmt.Fprintf(os.Stderr, "\nRetrying %d failed items...\n", len(failedItems))
		retried := 0
		for _, item := range failedItems {
			emb, err := embedder.EmbedDocument(ctx, item.text)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  retry failed: %s: %v\n", item.id, err)
				continue
			}
			if err := store.AddDocument(ctx, item.id, item.text, emb, item.meta); err != nil {
				fmt.Fprintf(os.Stderr, "  retry store failed: %s: %v\n", item.id, err)
				continue
			}
			embedCache.Set(item.id, item.contentHash)
			embedded++
			retried++
			if errors > 0 {
				errors--
			}
		}
		if retried > 0 {
			fmt.Fprintf(os.Stderr, "  Retried successfully: %d/%d\n", retried, len(failedItems))
		}
	}

	// Save the embed cache.
	if err := embedCache.Save(out); err != nil {
		return fmt.Errorf("saving embed cache: %w", err)
	}

	elapsed := time.Since(startTime)
	fmt.Fprintf(os.Stderr, "\nDone in %s: %d embedded, %d skipped", elapsed.Round(time.Second), embedded, skipped)
	if errors > 0 {
		fmt.Fprintf(os.Stderr, ", %d errors", errors)
	}
	if embedded > 0 {
		fmt.Fprintf(os.Stderr, " (%.1f items/sec)", float64(embedded)/elapsed.Seconds())
	}
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Total vectors in database: %d\n", store.Count())

	return nil
}

// quickHash returns a short hex hash of a string.
func quickHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}
