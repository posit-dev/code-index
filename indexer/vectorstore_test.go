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
