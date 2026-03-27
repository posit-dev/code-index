// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CacheDir is the subdirectory name for cached docs.
const CacheDir = "docs"

// LoadCacheManifest reads the cache manifest from the output directory.
// Returns an empty manifest if the file doesn't exist.
func LoadCacheManifest(outputDir string) (*CacheManifest, error) {
	path := filepath.Join(outputDir, "cache.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewCacheManifest(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading cache manifest: %w", err)
	}

	var manifest CacheManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parsing cache manifest: %w", err)
	}

	// Initialize nil maps.
	if manifest.Functions == nil {
		manifest.Functions = make(map[string]*FunctionCache)
	}
	if manifest.Files == nil {
		manifest.Files = make(map[string]*FileCache)
	}
	if manifest.Packages == nil {
		manifest.Packages = make(map[string]*PackageCache)
	}

	return &manifest, nil
}

// SaveCacheManifest writes the cache manifest to the output directory.
func SaveCacheManifest(outputDir string, manifest *CacheManifest) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cache manifest: %w", err)
	}

	path := filepath.Join(outputDir, "cache.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing cache manifest: %w", err)
	}

	return nil
}

// DiffResult describes what needs to be regenerated.
type DiffResult struct {
	// Functions that need doc regeneration, keyed by "file::FuncName".
	ChangedFunctions map[string]*FunctionInfo
	// Files that need doc regeneration.
	ChangedFiles map[string]*FileInfo
	// Packages that need doc regeneration.
	ChangedPackages map[string]*PackageInfo
	// Functions that were removed (clean up from cache).
	RemovedFunctions []string
	// Files that were removed.
	RemovedFiles []string
	// Packages that were removed.
	RemovedPackages []string
}

// NewDiffResult creates an empty diff result.
func NewDiffResult() *DiffResult {
	return &DiffResult{
		ChangedFunctions: make(map[string]*FunctionInfo),
		ChangedFiles:     make(map[string]*FileInfo),
		ChangedPackages:  make(map[string]*PackageInfo),
	}
}

// FunctionCacheKey returns the cache key for a function.
func FunctionCacheKey(filePath, funcName, receiver string) string {
	if receiver != "" {
		return filePath + "::" + receiver + "." + funcName
	}
	return filePath + "::" + funcName
}

// ComputeDiff compares the current parse result against the cache manifest
// and determines what needs to be regenerated.
func ComputeDiff(parsed *ParseResult, cache *CacheManifest) *DiffResult {
	diff := NewDiffResult()

	// Track which cache entries are still present.
	seenFunctions := make(map[string]bool)
	seenFiles := make(map[string]bool)
	seenPackages := make(map[string]bool)

	// Check functions for changes.
	for _, fileInfo := range parsed.Files {
		for i := range fileInfo.Functions {
			fn := &fileInfo.Functions[i]
			key := FunctionCacheKey(fn.File, fn.Name, fn.Receiver)
			seenFunctions[key] = true

			cached, exists := cache.Functions[key]
			if !exists || cached.ASTHash != fn.ASTHash {
				diff.ChangedFunctions[key] = fn
			}
		}
	}

	// Check files for changes — a file needs regeneration if any of its
	// functions changed or if its function set changed.
	for filePath, fileInfo := range parsed.Files {
		seenFiles[filePath] = true

		// Compute composite hash of current function AST hashes.
		var funcHashes []string
		for i := range fileInfo.Functions {
			funcHashes = append(funcHashes, fileInfo.Functions[i].ASTHash)
		}
		for i := range fileInfo.Types {
			funcHashes = append(funcHashes, fileInfo.Types[i].ASTHash)
		}
		currentFuncDocHash := hashStrings(funcHashes)

		cached, exists := cache.Files[filePath]
		if !exists || cached.FuncDocHash != currentFuncDocHash {
			diff.ChangedFiles[filePath] = fileInfo
		}
	}

	// Check packages for changes — a package needs regeneration if any of
	// its files changed.
	for importPath, pkgInfo := range parsed.Packages {
		seenPackages[importPath] = true

		var fileHashes []string
		for _, filePath := range pkgInfo.Files {
			if fi, ok := parsed.Files[filePath]; ok {
				fileHashes = append(fileHashes, fi.ASTHash)
			}
		}
		currentFileDocHash := hashStrings(fileHashes)

		cached, exists := cache.Packages[importPath]
		if !exists || cached.FileDocHash != currentFileDocHash {
			diff.ChangedPackages[importPath] = pkgInfo
		}
	}

	// Find removed entries.
	for key := range cache.Functions {
		if !seenFunctions[key] {
			diff.RemovedFunctions = append(diff.RemovedFunctions, key)
		}
	}
	for key := range cache.Files {
		if !seenFiles[key] {
			diff.RemovedFiles = append(diff.RemovedFiles, key)
		}
	}
	for key := range cache.Packages {
		if !seenPackages[key] {
			diff.RemovedPackages = append(diff.RemovedPackages, key)
		}
	}

	return diff
}
