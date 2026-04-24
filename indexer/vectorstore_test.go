// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"context"
	"testing"
)

func TestVectorStoreAddAndSearch(t *testing.T) {
	dir := t.TempDir()
	dims := 4 // tiny vectors for testing

	store, err := OpenVectorStore(dir, dims)
	if err != nil {
		t.Fatalf("OpenVectorStore: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Logf("warning: closing store: %v", err)
		}
	}()

	ctx := context.Background()

	// Add documents with known vectors.
	docs := []struct {
		id        string
		content   string
		embedding []float32
		meta      DocumentMetadata
	}{
		{
			id:        "func:cache.go:Get:32",
			content:   "Get retrieves a value",
			embedding: []float32{1, 0, 0, 0},
			meta:      DocumentMetadata{Kind: "function", Name: "Get", File: "cache.go", Line: 32},
		},
		{
			id:        "func:cache.go:Set:47",
			content:   "Set stores a value",
			embedding: []float32{0, 1, 0, 0},
			meta:      DocumentMetadata{Kind: "function", Name: "Set", File: "cache.go", Line: 47},
		},
		{
			id:        "type:cache.go:Cache:16",
			content:   "Cache is a thread-safe cache",
			embedding: []float32{0.5, 0.5, 0, 0},
			meta:      DocumentMetadata{Kind: "type", Name: "Cache", File: "cache.go", Line: 16},
		},
	}

	for _, d := range docs {
		if err := store.AddDocument(ctx, d.id, d.content, d.embedding, d.meta); err != nil {
			t.Fatalf("AddDocument(%s): %v", d.id, err)
		}
	}

	// Count should be 3.
	if got := store.Count(); got != 3 {
		t.Errorf("Count() = %d, want 3", got)
	}

	// Search with a vector close to "Get" — should return Get as top result.
	results, err := store.Search(ctx, []float32{0.9, 0.1, 0, 0}, 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}
	if results[0].Metadata["name"] != "Get" {
		t.Errorf("top result = %q, want %q", results[0].Metadata["name"], "Get")
	}
	if results[0].Similarity <= 0 {
		t.Error("similarity should be positive")
	}
}

func TestVectorStoreUpsert(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenVectorStore(dir, 4)
	if err != nil {
		t.Fatalf("OpenVectorStore: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Logf("warning: closing store: %v", err)
		}
	}()

	ctx := context.Background()
	meta := DocumentMetadata{Kind: "function", Name: "Get", File: "cache.go", Line: 32}

	// Insert.
	if err := store.AddDocument(ctx, "func:Get", "original", []float32{1, 0, 0, 0}, meta); err != nil {
		t.Fatalf("AddDocument: %v", err)
	}
	if got := store.Count(); got != 1 {
		t.Fatalf("Count() = %d, want 1", got)
	}

	// Upsert same ID with new content.
	meta.Name = "GetUpdated"
	if err := store.AddDocument(ctx, "func:Get", "updated", []float32{0, 1, 0, 0}, meta); err != nil {
		t.Fatalf("AddDocument (upsert): %v", err)
	}

	// Count should still be 1.
	if got := store.Count(); got != 1 {
		t.Errorf("Count() after upsert = %d, want 1", got)
	}

	// Search should return the updated metadata.
	results, err := store.Search(ctx, []float32{0, 1, 0, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}
	if results[0].Metadata["name"] != "GetUpdated" {
		t.Errorf("name = %q, want %q", results[0].Metadata["name"], "GetUpdated")
	}
}

func TestVectorStoreReset(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenVectorStore(dir, 4)
	if err != nil {
		t.Fatalf("OpenVectorStore: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Logf("warning: closing store: %v", err)
		}
	}()

	ctx := context.Background()
	meta := DocumentMetadata{Kind: "function", Name: "Get"}
	if err := store.AddDocument(ctx, "func:Get", "Get", []float32{1, 0, 0, 0}, meta); err != nil {
		t.Fatalf("AddDocument: %v", err)
	}

	if got := store.Count(); got != 1 {
		t.Fatalf("Count() = %d, want 1", got)
	}

	// Reset.
	if err := store.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if got := store.Count(); got != 0 {
		t.Errorf("Count() after Reset = %d, want 0", got)
	}
}

func TestVectorStoreDimensionMismatch(t *testing.T) {
	dir := t.TempDir()

	// Create with 4 dimensions.
	store, err := OpenVectorStore(dir, 4)
	if err != nil {
		t.Fatalf("OpenVectorStore(4): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen with different dimensions — should error.
	_, err = OpenVectorStore(dir, 8)
	if err == nil {
		t.Fatal("expected error for dimension mismatch, got nil")
	}

	// Reopen with 0 — should use stored dimension.
	store2, err := OpenVectorStore(dir, 0)
	if err != nil {
		t.Fatalf("OpenVectorStore(0): %v", err)
	}
	defer func() {
		if err := store2.Close(); err != nil {
			t.Logf("warning: closing store: %v", err)
		}
	}()

	if got := store2.Dimensions(); got != 4 {
		t.Errorf("Dimensions() = %d, want 4", got)
	}
}

func newTestStore(t *testing.T, dims int) *VectorStore {
	t.Helper()
	store, err := OpenVectorStore(t.TempDir(), dims)
	if err != nil {
		t.Fatalf("OpenVectorStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("warning: closing store: %v", err)
		}
	})
	return store
}

func addTestDoc(
	t *testing.T,
	store *VectorStore,
	id, content string,
	emb []float32,
	meta DocumentMetadata,
) {
	t.Helper()
	ctx := context.Background()
	if err := store.AddDocument(ctx, id, content, emb, meta); err != nil {
		t.Fatalf("AddDocument(%s): %v", id, err)
	}
}

func TestHybridSearchKeywordBoost(t *testing.T) {
	store := newTestStore(t, 4)
	ctx := context.Background()

	// WHEN two documents have identical embeddings but different text.
	emb := []float32{0.9, 0.1, 0, 0}
	addTestDoc(t, store, "func:a",
		"database connection pool manager", emb,
		DocumentMetadata{Kind: "function", Name: "ConnPool",
			File: "db.go", Summary: "manages database connection pooling"})
	addTestDoc(t, store, "func:b",
		"network socket handler", emb,
		DocumentMetadata{Kind: "function", Name: "SocketHandler",
			File: "net.go", Summary: "handles network socket connections"})

	// AND we search with a keyword that appears only in the first doc.
	results, err := store.HybridSearch(ctx, emb, "database pool", 2, 0.5)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}

	// THEN the keyword-matching document ranks first because BM25
	// breaks the vector-score tie.
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].Metadata["name"] != "ConnPool" {
		t.Errorf("top result = %q, want ConnPool",
			results[0].Metadata["name"])
	}
	if results[0].Score <= 0 {
		t.Error("Score should be positive")
	}
}

func TestHybridSearchNoKeywordMatches(t *testing.T) {
	store := newTestStore(t, 4)
	ctx := context.Background()

	addTestDoc(t, store, "func:a", "Get retrieves a value", []float32{1, 0, 0, 0},
		DocumentMetadata{Kind: "function", Name: "Get", File: "cache.go"})
	addTestDoc(t, store, "func:b", "Set stores a value", []float32{0, 1, 0, 0},
		DocumentMetadata{Kind: "function", Name: "Set", File: "cache.go"})

	// WHEN we search with a query that has no FTS5 matches.
	query := []float32{0.9, 0.1, 0, 0}
	hybrid, err := store.HybridSearch(
		ctx, query, "zzzznonexistent", 2, 0.6)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	vector, err := store.Search(ctx, query, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// THEN results match vector-only ordering.
	if len(hybrid) != len(vector) {
		t.Fatalf("result count: hybrid=%d, vector=%d",
			len(hybrid), len(vector))
	}
	for i := range hybrid {
		if hybrid[i].ID != vector[i].ID {
			t.Errorf("result[%d]: hybrid=%s, vector=%s",
				i, hybrid[i].ID, vector[i].ID)
		}
	}
}

func TestHybridSearchAlphaOne(t *testing.T) {
	store := newTestStore(t, 4)
	ctx := context.Background()

	addTestDoc(t, store, "func:a", "alpha function", []float32{1, 0, 0, 0},
		DocumentMetadata{Kind: "function", Name: "Alpha", File: "a.go"})
	addTestDoc(t, store, "func:b", "beta function", []float32{0, 1, 0, 0},
		DocumentMetadata{Kind: "function", Name: "Beta", File: "b.go"})

	query := []float32{0.9, 0.1, 0, 0}

	// WHEN alpha = 1.0 (pure vector).
	hybrid, err := store.HybridSearch(ctx, query, "beta", 2, 1.0)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	vector, err := store.Search(ctx, query, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// THEN results match exactly (delegates to Search).
	if len(hybrid) != len(vector) {
		t.Fatalf("result count: hybrid=%d, vector=%d",
			len(hybrid), len(vector))
	}
	for i := range hybrid {
		if hybrid[i].ID != vector[i].ID {
			t.Errorf("result[%d]: hybrid=%s, vector=%s",
				i, hybrid[i].ID, vector[i].ID)
		}
		if hybrid[i].Similarity != vector[i].Similarity {
			t.Errorf("result[%d] similarity: hybrid=%f, vector=%f",
				i, hybrid[i].Similarity, vector[i].Similarity)
		}
	}
}

func TestHybridSearchIdenticalVectorScores(t *testing.T) {
	store := newTestStore(t, 4)
	ctx := context.Background()

	// WHEN documents have identical embeddings but different text.
	emb := []float32{1, 0, 0, 0}
	addTestDoc(t, store, "func:a", "database migration tool", emb,
		DocumentMetadata{Kind: "function", Name: "Migrate",
			File: "db.go", Summary: "runs database migrations"})
	addTestDoc(t, store, "func:b", "network proxy setup", emb,
		DocumentMetadata{Kind: "function", Name: "ProxySetup",
			File: "net.go", Summary: "configures network proxy"})

	// AND we search with a keyword that matches only the first doc.
	results, err := store.HybridSearch(
		ctx, emb, "database migration", 2, 0.5)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}

	// THEN BM25 breaks the tie and the keyword-matching doc ranks first.
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Metadata["name"] != "Migrate" {
		t.Errorf("top result = %q, want Migrate",
			results[0].Metadata["name"])
	}
	// ALSO both should have valid hybrid scores.
	if results[0].Score <= results[1].Score {
		t.Errorf("expected first score > second: %f <= %f",
			results[0].Score, results[1].Score)
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "simple terms",
			input: "hello world",
			want:  "hello OR world",
		},
		{
			name:  "special characters stripped",
			input: `"hello*" (world) {test} +foo -bar`,
			want:  "hello OR world OR test OR foo OR bar",
		},
		{
			name: "reserved words removed",
			input: "hello AND world OR NOT test NEAR end",
			want:  "hello OR world OR test OR end",
		},
		{
			name:  "empty after sanitization",
			input: `"" AND OR NOT`,
			want:  "",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name: "mixed case reserved words preserved",
			input: "And or not hello",
			want:  "hello",
		},
		{
			name:  "only special chars",
			input: `*"(){}+-`,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

