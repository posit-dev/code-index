// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"path/filepath"
	"runtime"
	"testing"
)

// testdataDir returns the absolute path to the testdata directory.
func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata")
}

// findFunc returns the FunctionInfo with the given name, or nil.
func findFunc(funcs []FunctionInfo, name string) *FunctionInfo {
	for i := range funcs {
		if funcs[i].Name == name {
			return &funcs[i]
		}
	}
	return nil
}

// findType returns the TypeInfo with the given name, or nil.
func findType(types []TypeInfo, name string) *TypeInfo {
	for i := range types {
		if types[i].Name == name {
			return &types[i]
		}
	}
	return nil
}

// --- Go Parser ---

func TestGoParser(t *testing.T) {
	srcRoot := filepath.Join(testdataDir(), "go")
	parser := NewParser(srcRoot, "github.com/example/testdata")
	result, err := parser.Parse()
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	// Should have at least one file.
	if len(result.Files) == 0 {
		t.Fatal("expected at least one file parsed")
	}

	// Find cache.go.
	var fileInfo *FileInfo
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "cache.go" {
			fileInfo = f
			break
		}
	}
	if fileInfo == nil {
		t.Fatal("cache.go not found in parsed files")
	}

	// Check package name.
	if fileInfo.Package != "cache" {
		t.Errorf("Package = %q, want %q", fileInfo.Package, "cache")
	}

	// Check functions.
	expectedFuncs := []struct {
		name     string
		receiver string
		exported bool
	}{
		{"New", "", true},
		{"Get", "*Cache", true},
		{"Set", "*Cache", true},
		{"Delete", "*Cache", true},
		{"Size", "*Cache", true},
		{"Purge", "*Cache", true},
	}

	for _, exp := range expectedFuncs {
		fn := findFunc(fileInfo.Functions, exp.name)
		if fn == nil {
			t.Errorf("function %s not found", exp.name)
			continue
		}
		if fn.Exported != exp.exported {
			t.Errorf("function %s: Exported = %v, want %v", exp.name, fn.Exported, exp.exported)
		}
		if fn.Receiver != exp.receiver {
			t.Errorf("function %s: Receiver = %q, want %q", exp.name, fn.Receiver, exp.receiver)
		}
		if fn.Line == 0 {
			t.Errorf("function %s: Line = 0, want non-zero", exp.name)
		}
		if fn.Signature == "" {
			t.Errorf("function %s: Signature is empty", exp.name)
		}
	}

	// Check types.
	entry := findType(fileInfo.Types, "Entry")
	if entry == nil {
		t.Fatal("type Entry not found")
	}
	if entry.Kind != "struct" {
		t.Errorf("Entry.Kind = %q, want %q", entry.Kind, "struct")
	}
	if !entry.Exported {
		t.Error("Entry should be exported")
	}

	cache := findType(fileInfo.Types, "Cache")
	if cache == nil {
		t.Fatal("type Cache not found")
	}
	if cache.Kind != "struct" {
		t.Errorf("Cache.Kind = %q, want %q", cache.Kind, "struct")
	}
}

// --- TypeScript Parser ---

func TestTypeScriptParser(t *testing.T) {
	srcRoot := filepath.Join(testdataDir(), "typescript")
	parser := NewTSParser(srcRoot, nil)
	result := NewParseResult()
	if err := parser.Parse(result); err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("expected at least one file parsed")
	}

	var fileInfo *FileInfo
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "api-client.ts" {
			fileInfo = f
			break
		}
	}
	if fileInfo == nil {
		t.Fatal("api-client.ts not found in parsed files")
	}

	// Should have exported functions.
	if len(fileInfo.Functions) == 0 {
		t.Fatal("expected functions in api-client.ts")
	}

	// Check for createClient.
	fn := findFunc(fileInfo.Functions, "createClient")
	if fn == nil {
		t.Error("function createClient not found")
	} else {
		if !fn.Exported {
			t.Error("createClient should be exported")
		}
		if fn.Line == 0 {
			t.Error("createClient: Line = 0, want non-zero")
		}
	}

	// Check for types.
	if len(fileInfo.Types) == 0 {
		t.Fatal("expected types in api-client.ts")
	}

	apiConfig := findType(fileInfo.Types, "ApiClientConfig")
	if apiConfig == nil {
		t.Error("type ApiClientConfig not found")
	} else if apiConfig.Kind != "interface" {
		t.Errorf("ApiClientConfig.Kind = %q, want %q", apiConfig.Kind, "interface")
	}
}

// --- Vue SFC Parser ---

func TestVueParser(t *testing.T) {
	srcRoot := filepath.Join(testdataDir(), "vue")
	parser := NewTSParser(srcRoot, nil)
	result := NewParseResult()
	if err := parser.Parse(result); err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("expected at least one file parsed")
	}

	var fileInfo *FileInfo
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "SearchBar.vue" {
			fileInfo = f
			break
		}
	}
	if fileInfo == nil {
		t.Fatal("SearchBar.vue not found in parsed files")
	}

	// Vue files should produce functions from the <script> block.
	if len(fileInfo.Functions) == 0 {
		t.Error("expected functions in SearchBar.vue")
	}
}

// --- Python Parser ---

func TestPythonParser(t *testing.T) {
	srcRoot := filepath.Join(testdataDir(), "python")
	parser := NewPythonParser(srcRoot, nil)
	result := NewParseResult()
	if err := parser.Parse(result); err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("expected at least one file parsed")
	}

	var fileInfo *FileInfo
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "data_pipeline.py" {
			fileInfo = f
			break
		}
	}
	if fileInfo == nil {
		t.Fatal("data_pipeline.py not found in parsed files")
	}

	// Check for Pipeline class.
	pipeline := findType(fileInfo.Types, "Pipeline")
	if pipeline == nil {
		t.Error("type Pipeline not found")
	} else if pipeline.Kind != "class" {
		t.Errorf("Pipeline.Kind = %q, want %q", pipeline.Kind, "class")
	}

	// Check for TransformError class.
	transformErr := findType(fileInfo.Types, "TransformError")
	if transformErr == nil {
		t.Error("type TransformError not found")
	}

	// Check for standalone functions.
	filterStep := findFunc(fileInfo.Functions, "filter_step")
	if filterStep == nil {
		t.Error("function filter_step not found")
	}

	mapField := findFunc(fileInfo.Functions, "map_field")
	if mapField == nil {
		t.Error("function map_field not found")
	}
}

// --- C/C++ Parser ---

func TestCParser(t *testing.T) {
	srcRoot := filepath.Join(testdataDir(), "c")
	parser := NewCParser(srcRoot, nil)
	result := NewParseResult()
	if err := parser.Parse(result); err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("expected at least one file parsed")
	}

	// Check header file for type definitions.
	var headerInfo *FileInfo
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "hash_table.h" {
			headerInfo = f
			break
		}
	}
	if headerInfo == nil {
		t.Fatal("hash_table.h not found in parsed files")
	}

	// Should have struct types.
	if len(headerInfo.Types) == 0 {
		t.Error("expected types in hash_table.h")
	}

	hashTable := findType(headerInfo.Types, "HashTable")
	if hashTable == nil {
		t.Error("type HashTable not found in header")
	}

	// Should have function declarations.
	if len(headerInfo.Functions) == 0 {
		t.Error("expected function declarations in hash_table.h")
	}

	create := findFunc(headerInfo.Functions, "hash_table_create")
	if create == nil {
		t.Error("function hash_table_create not found in header")
	}

	// Check C++ file.
	var cppInfo *FileInfo
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "string_pool.cpp" {
			cppInfo = f
			break
		}
	}
	if cppInfo == nil {
		t.Fatal("string_pool.cpp not found in parsed files")
	}

	// Should have StringPool class.
	stringPool := findType(cppInfo.Types, "StringPool")
	if stringPool == nil {
		t.Error("type StringPool not found in string_pool.cpp")
	} else if stringPool.Kind != "class" {
		t.Errorf("StringPool.Kind = %q, want %q", stringPool.Kind, "class")
	}
}

// --- R Parser ---

func TestRParser(t *testing.T) {
	srcRoot := filepath.Join(testdataDir(), "r")
	// Use regex fallback (no Rscript dependency in tests).
	parser := NewRParserWithConfig(srcRoot, nil, "", "")
	result := NewParseResult()
	if err := parser.Parse(result); err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("expected at least one file parsed")
	}

	var fileInfo *FileInfo
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "statistics.R" {
			fileInfo = f
			break
		}
	}
	if fileInfo == nil {
		t.Fatal("statistics.R not found in parsed files")
	}

	// Check for expected functions.
	expectedFuncs := []string{"weighted_mean", "moving_average", "se_mean", "normalize"}
	for _, name := range expectedFuncs {
		fn := findFunc(fileInfo.Functions, name)
		if fn == nil {
			t.Errorf("function %s not found", name)
			continue
		}
		if fn.Line == 0 {
			t.Errorf("function %s: Line = 0, want non-zero", name)
		}
	}

	// weighted_mean should have roxygen doc.
	wm := findFunc(fileInfo.Functions, "weighted_mean")
	if wm != nil && wm.Doc == "" {
		t.Error("weighted_mean should have a doc comment from roxygen")
	}
}
