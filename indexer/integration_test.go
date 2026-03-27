// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// mockEmbedder returns deterministic embeddings based on content hash.
type mockEmbedder struct {
	dims int
}

func (m *mockEmbedder) Name() string { return "mock" }

func (m *mockEmbedder) EmbedDocument(_ context.Context, text string) ([]float32, error) {
	return m.deterministicVec(text), nil
}

func (m *mockEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return m.deterministicVec(text), nil
}

// deterministicVec generates a reproducible vector from text.
// Uses a simple hash-based approach so similar inputs don't get similar vectors,
// but the same input always gets the same vector.
func (m *mockEmbedder) deterministicVec(text string) []float32 {
	vec := make([]float32, m.dims)
	// Simple hash distribution.
	h := uint32(0)
	for _, c := range text {
		h = h*31 + uint32(c)
	}
	for i := range vec {
		h = h*1103515245 + 12345
		vec[i] = float32(h%1000) / 1000.0
	}
	// Normalize to unit vector for cosine similarity.
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
	return vec
}

// TestIntegrationPipeline tests the full parse → build → embed → search pipeline
// using testdata fixtures, mock summaries, and a mock embedder (no AWS needed).
func TestIntegrationPipeline(t *testing.T) {
	outputDir := t.TempDir()

	// --- Step 1: Parse the Go testdata ---
	goSrcRoot := filepath.Join(testdataDir(), "go")
	goParser := NewParser(goSrcRoot, "github.com/example/test")
	result, err := goParser.Parse()
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("parse produced no files")
	}

	// Write parsed.json (like the parse command does).
	parsedData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("marshal parsed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "parsed.json"), parsedData, 0o644); err != nil {
		t.Fatalf("write parsed.json: %v", err)
	}

	// --- Step 2: Write mock summaries (skip LLM) ---
	docsDir := filepath.Join(outputDir, CacheDir)
	for _, sub := range []string{"func", "file", "pkg"} {
		if err := os.MkdirAll(filepath.Join(docsDir, sub), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	// Write mock function docs.
	for _, f := range result.Files {
		for _, fn := range f.Functions {
			key := FunctionCacheKey(fn.File, fn.Name, fn.Receiver)
			doc := fmt.Sprintf("Mock summary for %s", fn.Name)
			path := funcDocPath(docsDir, key)
			if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
				t.Fatalf("write func doc: %v", err)
			}
		}
	}

	// Write mock file docs.
	for filePath := range result.Files {
		doc := fmt.Sprintf("Mock file summary for %s", filePath)
		path := fileDocPath(docsDir, filePath)
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatalf("write file doc: %v", err)
		}
	}

	// --- Step 3: Build the index ---
	index, err := BuildIndex(result, outputDir)
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	if len(index.Functions) == 0 {
		t.Fatal("index has no functions")
	}

	// Verify function summaries came through.
	hasSummary := false
	for _, fn := range index.Functions {
		if fn.Summary != "" {
			hasSummary = true
			break
		}
	}
	if !hasSummary {
		t.Error("no function summaries in index — mock docs not loaded")
	}

	// Write index.json.
	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "index.json"), indexData, 0o644); err != nil {
		t.Fatalf("write index.json: %v", err)
	}

	// --- Step 4: Embed with mock embedder ---
	dims := 32
	embedder := &mockEmbedder{dims: dims}
	ctx := context.Background()

	store, err := OpenVectorStore(outputDir, dims)
	if err != nil {
		t.Fatalf("OpenVectorStore: %v", err)
	}
	defer store.Close()

	embedded := 0
	for _, fn := range index.Functions {
		if !fn.Exported {
			continue
		}
		id := fmt.Sprintf("func:%s:%s:%d", fn.File, fn.Name, fn.Line)
		text := BuildEmbeddingText(fn.Name, fn.Signature, fn.Summary, fn.Doc, fn.File)
		emb, err := embedder.EmbedDocument(ctx, text)
		if err != nil {
			t.Fatalf("EmbedDocument: %v", err)
		}
		meta := DocumentMetadata{
			Kind:      "function",
			Name:      fn.Name,
			Signature: fn.Signature,
			File:      fn.File,
			Line:      fn.Line,
			Summary:   fn.Summary,
		}
		if err := store.AddDocument(ctx, id, text, emb, meta); err != nil {
			t.Fatalf("AddDocument: %v", err)
		}
		embedded++
	}

	if embedded == 0 {
		t.Fatal("no functions were embedded")
	}
	t.Logf("Embedded %d functions", embedded)

	// --- Step 5: Search ---
	queryText := "cache retrieval"
	queryVec, err := embedder.EmbedQuery(ctx, queryText)
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}

	results, err := store.Search(ctx, queryVec, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("search returned no results")
	}

	t.Logf("Search for %q returned %d results:", queryText, len(results))
	for i, r := range results {
		t.Logf("  %d. [%s] %s (%.1f%%)", i+1, r.Metadata["kind"], r.Metadata["name"], r.Similarity*100)
	}

	// Verify results have expected metadata fields.
	first := results[0]
	if first.Metadata["kind"] != "function" {
		t.Errorf("first result kind = %q, want %q", first.Metadata["kind"], "function")
	}
	if first.Metadata["name"] == "" {
		t.Error("first result has no name")
	}
	if first.Metadata["file"] == "" {
		t.Error("first result has no file")
	}
}
