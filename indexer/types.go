// Copyright (C) 2026 by Posit Software, PBC
package indexer

import "time"

// PackageInfo holds parsed information about a package or module.
type PackageInfo struct {
	// ImportPath is the full import path or directory path.
	ImportPath string `json:"import_path"`
	// Dir is the relative directory path from the source root.
	Dir string `json:"dir"`
	// Doc is the package-level doc comment.
	Doc string `json:"doc,omitempty"`
	// Files in this package (relative paths).
	Files []string `json:"files"`
	// ASTHash is a hash of the package's structural content.
	ASTHash string `json:"ast_hash"`
}

// FileInfo holds parsed information about a single source file.
type FileInfo struct {
	// Path is the relative file path from the source root.
	Path string `json:"path"`
	// Package is the package or module name.
	Package string `json:"package"`
	// ImportPath is the full import path of the containing package.
	ImportPath string `json:"import_path"`
	// Doc is the file-level doc comment or description.
	Doc string `json:"doc,omitempty"`
	// Functions defined in this file.
	Functions []FunctionInfo `json:"functions,omitempty"`
	// Types defined in this file.
	Types []TypeInfo `json:"types,omitempty"`
	// ASTHash is a hash of all function and type hashes in this file.
	ASTHash string `json:"ast_hash"`
}

// FunctionInfo holds parsed information about a function or method.
type FunctionInfo struct {
	// Name is the function name.
	Name string `json:"name"`
	// Receiver is the receiver type for methods (empty for functions).
	Receiver string `json:"receiver,omitempty"`
	// Signature is the full function signature.
	Signature string `json:"signature"`
	// Doc is the documentation comment.
	Doc string `json:"doc,omitempty"`
	// File is the relative file path.
	File string `json:"file"`
	// Line is the starting line number.
	Line int `json:"line"`
	// Exported indicates whether the function is exported/public.
	Exported bool `json:"exported"`
	// ASTHash is a hash of the normalized function AST.
	ASTHash string `json:"ast_hash"`
	// SigHash is a hash of just the function signature (params + returns).
	SigHash string `json:"sig_hash"`
	// Body is the function body text, included when under the size cap.
	Body string `json:"body,omitempty"`
	// Returns contains return expressions extracted from the function body.
	Returns []string `json:"returns,omitempty"`
	// Calls contains deduplicated callee expressions from the function body.
	Calls []string `json:"calls,omitempty"`
}

// TypeInfo holds parsed information about a type declaration.
type TypeInfo struct {
	// Name is the type name.
	Name string `json:"name"`
	// Kind describes the type (e.g., "struct", "interface", "class", "enum", "typedef").
	Kind string `json:"kind"`
	// Signature is the full type signature (e.g., C++ template parameters like "template<typename T>").
	Signature string `json:"signature,omitempty"`
	// Doc is the documentation comment.
	Doc string `json:"doc,omitempty"`
	// File is the relative file path.
	File string `json:"file"`
	// Line is the starting line number.
	Line int `json:"line"`
	// Exported indicates whether the type is exported.
	Exported bool `json:"exported"`
	// Methods associated with this type (populated during parsing).
	Methods []FunctionInfo `json:"methods,omitempty"`
	// Fields for struct types.
	Fields []FieldInfo `json:"fields,omitempty"`
	// ASTHash is a hash of the normalized type AST.
	ASTHash string `json:"ast_hash"`
}

// FieldInfo holds information about a struct field.
type FieldInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Tag  string `json:"tag,omitempty"`
	Doc  string `json:"doc,omitempty"`
}

// ParseResult holds the complete result of parsing the source tree.
type ParseResult struct {
	// Packages keyed by import path.
	Packages map[string]*PackageInfo `json:"packages"`
	// Files keyed by relative path.
	Files map[string]*FileInfo `json:"files"`
	// Timestamp of when parsing was performed.
	ParsedAt time.Time `json:"parsed_at"`
}

// CacheManifest tracks what has been indexed for incremental updates.
type CacheManifest struct {
	// Commit is the git commit SHA that was last indexed.
	Commit string `json:"commit"`
	// Functions tracks per-function cache state.
	Functions map[string]*FunctionCache `json:"functions"`
	// Files tracks per-file cache state.
	Files map[string]*FileCache `json:"files"`
	// Packages tracks per-package cache state.
	Packages map[string]*PackageCache `json:"packages"`
}

// FunctionCache tracks cache state for a single function.
type FunctionCache struct {
	ASTHash       string    `json:"ast_hash"`
	SigHash       string    `json:"sig_hash"`
	DocHash       string    `json:"doc_hash"`
	LastGenerated time.Time `json:"last_generated"`
}

// FileCache tracks cache state for a single file.
type FileCache struct {
	FuncDocHash   string    `json:"func_doc_hash"`
	LastGenerated time.Time `json:"last_generated"`
}

// PackageCache tracks cache state for a single package.
type PackageCache struct {
	FileDocHash   string    `json:"file_doc_hash"`
	LastGenerated time.Time `json:"last_generated"`
}

// NewCacheManifest creates an empty cache manifest.
func NewCacheManifest() *CacheManifest {
	return &CacheManifest{
		Functions: make(map[string]*FunctionCache),
		Files:     make(map[string]*FileCache),
		Packages:  make(map[string]*PackageCache),
	}
}

// NewParseResult creates an empty parse result.
func NewParseResult() *ParseResult {
	return &ParseResult{
		Packages: make(map[string]*PackageInfo),
		Files:    make(map[string]*FileInfo),
		ParsedAt: time.Now(),
	}
}
