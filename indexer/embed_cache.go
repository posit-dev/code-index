// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EmbedCache tracks which items have been embedded and with what content hash.
// This allows incremental updates — only items whose content has changed
// need to be re-embedded.
type EmbedCache struct {
	// Items maps document ID to the hash of the content that was embedded.
	Items map[string]string `json:"items"`
}

// NewEmbedCache creates an empty embed cache.
func NewEmbedCache() *EmbedCache {
	return &EmbedCache{
		Items: make(map[string]string),
	}
}

// LoadEmbedCache reads the embed cache from disk.
// Returns an empty cache if the file doesn't exist.
func LoadEmbedCache(outputDir string) (*EmbedCache, error) {
	path := embedCachePath(outputDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewEmbedCache(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading embed cache: %w", err)
	}

	var cache EmbedCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("parsing embed cache: %w", err)
	}
	if cache.Items == nil {
		cache.Items = make(map[string]string)
	}
	return &cache, nil
}

// Save writes the embed cache to disk.
func (c *EmbedCache) Save(outputDir string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling embed cache: %w", err)
	}
	path := embedCachePath(outputDir)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing embed cache: %w", err)
	}
	return nil
}

// IsUpToDate returns true if the item has already been embedded with the same content hash.
func (c *EmbedCache) IsUpToDate(id, contentHash string) bool {
	return c.Items[id] == contentHash
}

// Set records that an item has been embedded with the given content hash.
func (c *EmbedCache) Set(id, contentHash string) {
	c.Items[id] = contentHash
}

// Remove deletes an item from the cache.
func (c *EmbedCache) Remove(id string) {
	delete(c.Items, id)
}

func embedCachePath(outputDir string) string {
	return filepath.Join(outputDir, "embed_cache.json")
}
