// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Parser extracts structured information from Go source files using AST analysis.
type Parser struct {
	// srcRoot is the absolute path to the Go source root directory.
	srcRoot string
	// modulePrefix is the Go module path (e.g., "myproject").
	modulePrefix string
	// excludes is a list of filename patterns to skip.
	excludes []string
	// vendorIncludes lists Go module prefixes to index from vendor/.
	vendorIncludes []string
	// repoRoot is the absolute path to the repository root (parent of src/).
	repoRoot string
	fset     *token.FileSet
}

// NewParser creates a new AST parser for the given source root.
// modulePrefix is the Go module name (e.g., "myproject").
func NewParser(srcRoot, modulePrefix string) *Parser {
	return &Parser{
		srcRoot:      srcRoot,
		modulePrefix: modulePrefix,
		repoRoot:     filepath.Dir(srcRoot),
		fset:         token.NewFileSet(),
	}
}

// NewParserWithConfig creates a parser with explicit excludes and vendor includes.
func NewParserWithConfig(srcRoot, modulePrefix string, excludes, vendorIncludes []string) *Parser {
	return &Parser{
		srcRoot:        srcRoot,
		modulePrefix:   modulePrefix,
		excludes:       excludes,
		vendorIncludes: vendorIncludes,
		repoRoot:       filepath.Dir(srcRoot),
		fset:           token.NewFileSet(),
	}
}

// Parse walks the source tree and extracts all package, file, function, and type information.
// Returns a new ParseResult.
func (p *Parser) Parse() (*ParseResult, error) {
	result := NewParseResult()
	if err := p.ParseInto(result); err != nil {
		return nil, err
	}
	return result, nil
}

// ParseInto walks the source tree and adds results to an existing ParseResult.
// This allows multiple parsers to contribute to the same result.
func (p *Parser) ParseInto(result *ParseResult) error {
	// Parse the main source tree.
	if err := p.parseTree(p.srcRoot, result); err != nil {
		return err
	}

	// Parse vendored libraries if configured.
	if len(p.vendorIncludes) > 0 {
		fmt.Fprintf(os.Stderr, "  Scanning vendored modules...\n")
		if err := p.parseVendorIncludes(result); err != nil {
			return err
		}
	}

	// Build package-level info from collected files.
	p.buildPackageInfo(result)

	return nil
}

// parseTree walks a directory tree and parses Go files.
func (p *Parser) parseTree(root string, result *ParseResult) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip non-directories and hidden/vendor directories.
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "testdata" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .go files, skip test files.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		base := filepath.Base(path)
		// Check excludes.
		for _, pattern := range p.excludes {
			matched, err := filepath.Match(pattern, base)
			if err != nil {
				continue
			}
			if matched {
				return nil
			}
		}

		return p.parseFile(path, result)
	})
	return err
}

// parseVendorIncludes indexes Go files from specified vendored modules.
func (p *Parser) parseVendorIncludes(result *ParseResult) error {
	vendorDir := filepath.Join(p.repoRoot, "vendor")
	if _, err := os.Stat(vendorDir); os.IsNotExist(err) {
		return nil
	}

	for _, prefix := range p.vendorIncludes {
		vendorPath := filepath.Join(vendorDir, filepath.FromSlash(prefix))
		if _, err := os.Stat(vendorPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "  warning: vendored module not found: %s\n", prefix)
			continue
		}

		fmt.Fprintf(os.Stderr, "  Indexing vendored module: %s\n", prefix)

		err := filepath.Walk(vendorPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				base := filepath.Base(path)
				if base == "testdata" || strings.HasPrefix(base, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			return p.parseVendorFile(path, prefix, vendorDir, result)
		})
		if err != nil {
			return fmt.Errorf("walking vendor path %s: %w", prefix, err)
		}
	}
	return nil
}

// parseVendorFile parses a Go file from vendor/ and uses the module path as the import prefix.
func (p *Parser) parseVendorFile(path, modulePrefix, vendorDir string, result *ParseResult) error {
	relPath, err := filepath.Rel(vendorDir, path)
	if err != nil {
		return nil
	}

	file, err := parser.ParseFile(p.fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	importPath := filepath.ToSlash(filepath.Dir(relPath))
	// Use "vendor/<module>" as the file path prefix so it's distinguishable.
	fileRelPath := "vendor/" + filepath.ToSlash(relPath)

	fileInfo := &FileInfo{
		Path:       fileRelPath,
		Package:    file.Name.Name,
		ImportPath: importPath,
		Doc:        extractFileDoc(file),
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			funcInfo := p.extractFunction(d, fileRelPath)
			fileInfo.Functions = append(fileInfo.Functions, funcInfo)
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					typeInfo := p.extractType(d, ts, fileRelPath)
					fileInfo.Types = append(fileInfo.Types, typeInfo)
				}
			}
		}
	}

	fileInfo.ASTHash = p.computeFileHash(fileInfo)
	result.Files[fileInfo.Path] = fileInfo
	return nil
}

// parseFile parses a single Go file and adds its information to the result.
func (p *Parser) parseFile(path string, result *ParseResult) error {
	relPath, err := filepath.Rel(p.srcRoot, path)
	if err != nil {
		return fmt.Errorf("computing relative path for %s: %w", path, err)
	}

	file, err := parser.ParseFile(p.fset, path, nil, parser.ParseComments)
	if err != nil {
		// Skip files that can't be parsed.
		fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", relPath, err)
		return nil
	}

	dir := filepath.Dir(relPath)
	importPath := p.modulePrefix + "/" + filepath.ToSlash(dir)

	fileInfo := &FileInfo{
		Path:       filepath.ToSlash(relPath),
		Package:    file.Name.Name,
		ImportPath: importPath,
		Doc:        extractFileDoc(file),
	}

	// Extract functions and methods.
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			funcInfo := p.extractFunction(d, fileInfo.Path)
			fileInfo.Functions = append(fileInfo.Functions, funcInfo)
		case *ast.GenDecl:
			if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					typeInfo := p.extractType(d, ts, fileInfo.Path)
					fileInfo.Types = append(fileInfo.Types, typeInfo)
				}
			}
		}
	}

	// Compute file-level AST hash from function and type hashes.
	fileInfo.ASTHash = p.computeFileHash(fileInfo)

	result.Files[fileInfo.Path] = fileInfo
	return nil
}

// extractFunction extracts information from a function declaration.
func (p *Parser) extractFunction(decl *ast.FuncDecl, filePath string) FunctionInfo {
	info := FunctionInfo{
		Name:     decl.Name.Name,
		Doc:      extractDoc(decl.Doc),
		File:     filePath,
		Line:     p.fset.Position(decl.Pos()).Line,
		Exported: ast.IsExported(decl.Name.Name),
	}

	// Extract receiver for methods.
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		info.Receiver = typeExprString(decl.Recv.List[0].Type)
	}

	// Build signature string.
	info.Signature = p.buildSignature(decl)

	// Compute AST hashes.
	info.ASTHash = p.hashFuncDecl(decl)
	info.SigHash = p.hashFuncSignature(decl)

	return info
}

// extractType extracts information from a type declaration.
func (p *Parser) extractType(genDecl *ast.GenDecl, spec *ast.TypeSpec, filePath string) TypeInfo {
	info := TypeInfo{
		Name:     spec.Name.Name,
		Doc:      extractDoc(genDecl.Doc),
		File:     filePath,
		Line:     p.fset.Position(spec.Pos()).Line,
		Exported: ast.IsExported(spec.Name.Name),
	}

	// If the GenDecl doc is empty, try the TypeSpec doc.
	if info.Doc == "" {
		info.Doc = extractDoc(spec.Doc)
	}

	switch t := spec.Type.(type) {
	case *ast.StructType:
		info.Kind = "struct"
		info.Fields = extractFields(t.Fields)
	case *ast.InterfaceType:
		info.Kind = "interface"
	default:
		info.Kind = "other"
	}

	info.ASTHash = p.hashTypeSpec(spec)

	return info
}

// buildSignature constructs a human-readable function signature.
func (p *Parser) buildSignature(decl *ast.FuncDecl) string {
	var sb strings.Builder
	sb.WriteString("func ")

	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		sb.WriteString("(")
		sb.WriteString(typeExprString(decl.Recv.List[0].Type))
		sb.WriteString(") ")
	}

	sb.WriteString(decl.Name.Name)
	sb.WriteString("(")
	sb.WriteString(fieldListString(decl.Type.Params))
	sb.WriteString(")")

	if decl.Type.Results != nil && len(decl.Type.Results.List) > 0 {
		results := fieldListString(decl.Type.Results)
		if len(decl.Type.Results.List) == 1 && len(decl.Type.Results.List[0].Names) == 0 {
			sb.WriteString(" ")
			sb.WriteString(results)
		} else {
			sb.WriteString(" (")
			sb.WriteString(results)
			sb.WriteString(")")
		}
	}

	return sb.String()
}

// buildPackageInfo aggregates file information into package-level data.
func (p *Parser) buildPackageInfo(result *ParseResult) {
	pkgFiles := make(map[string][]string) // importPath -> []filePath

	for filePath, fileInfo := range result.Files {
		pkgFiles[fileInfo.ImportPath] = append(pkgFiles[fileInfo.ImportPath], filePath)
	}

	for importPath, files := range pkgFiles {
		sort.Strings(files)

		// Find the package doc from the first file that has one.
		var pkgDoc string
		for _, f := range files {
			if doc := result.Files[f].Doc; doc != "" {
				pkgDoc = doc
				break
			}
		}

		// Compute directory relative to src root.
		dir := strings.TrimPrefix(importPath, p.modulePrefix+"/")

		// Compute package hash from file hashes.
		var fileHashes []string
		for _, f := range files {
			fileHashes = append(fileHashes, result.Files[f].ASTHash)
		}

		result.Packages[importPath] = &PackageInfo{
			ImportPath: importPath,
			Dir:        dir,
			Doc:        pkgDoc,
			Files:      files,
			ASTHash:    hashStrings(fileHashes),
		}
	}
}

// hashFuncDecl computes a normalized hash of a function declaration.
// This hash is stable across formatting and comment changes.
func (p *Parser) hashFuncDecl(decl *ast.FuncDecl) string {
	// Create a copy without comments to normalize.
	normalized := &ast.FuncDecl{
		Recv: decl.Recv,
		Name: decl.Name,
		Type: decl.Type,
		Body: decl.Body,
	}
	return p.hashNode(normalized)
}

// hashFuncSignature computes a hash of just the function signature
// (receiver, params, return types) — not the body.
func (p *Parser) hashFuncSignature(decl *ast.FuncDecl) string {
	sigDecl := &ast.FuncDecl{
		Recv: decl.Recv,
		Name: decl.Name,
		Type: decl.Type,
	}
	return p.hashNode(sigDecl)
}

// hashTypeSpec computes a hash of a type specification.
func (p *Parser) hashTypeSpec(spec *ast.TypeSpec) string {
	normalized := &ast.TypeSpec{
		Name: spec.Name,
		Type: spec.Type,
	}
	return p.hashNode(normalized)
}

// hashNode prints an AST node and hashes the output.
func (p *Parser) hashNode(node ast.Node) string {
	var sb strings.Builder
	// Use a fresh file set to avoid position-dependent output.
	cfg := printer.Config{Mode: printer.RawFormat}
	_ = cfg.Fprint(&sb, token.NewFileSet(), node)
	return hashString(sb.String())
}

// computeFileHash computes a hash for the file from its function and type hashes.
func (p *Parser) computeFileHash(info *FileInfo) string {
	var hashes []string
	for i := range info.Functions {
		hashes = append(hashes, info.Functions[i].ASTHash)
	}
	for i := range info.Types {
		hashes = append(hashes, info.Types[i].ASTHash)
	}
	return hashStrings(hashes)
}

// extractFileDoc gets the package-level doc comment from a file.
// Filters out copyright headers which are not useful as documentation.
func extractFileDoc(file *ast.File) string {
	if file.Doc == nil {
		return ""
	}
	text := strings.TrimSpace(file.Doc.Text())
	// Skip copyright-only doc comments.
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "copyright") {
		return ""
	}
	return text
}

// extractDoc gets the text from a comment group.
func extractDoc(cg *ast.CommentGroup) string {
	if cg == nil {
		return ""
	}
	return strings.TrimSpace(cg.Text())
}

// extractFields extracts field information from a struct's field list.
func extractFields(fields *ast.FieldList) []FieldInfo {
	if fields == nil {
		return nil
	}
	var result []FieldInfo
	for _, field := range fields.List {
		typeStr := typeExprString(field.Type)
		doc := extractDoc(field.Doc)
		tag := ""
		if field.Tag != nil {
			tag = field.Tag.Value
		}

		if len(field.Names) == 0 {
			// Embedded field.
			result = append(result, FieldInfo{
				Name: typeStr,
				Type: typeStr,
				Tag:  tag,
				Doc:  doc,
			})
		} else {
			for _, name := range field.Names {
				if !unicode.IsUpper(rune(name.Name[0])) {
					continue // Skip unexported fields.
				}
				result = append(result, FieldInfo{
					Name: name.Name,
					Type: typeStr,
					Tag:  tag,
					Doc:  doc,
				})
			}
		}
	}
	return result
}

// typeExprString converts a type expression AST node to a string.
func typeExprString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeExprString(t.X)
	case *ast.SelectorExpr:
		return typeExprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + typeExprString(t.Elt)
		}
		return "[...]" + typeExprString(t.Elt)
	case *ast.MapType:
		return "map[" + typeExprString(t.Key) + "]" + typeExprString(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.ChanType:
		return "chan " + typeExprString(t.Value)
	case *ast.FuncType:
		return "func(...)"
	case *ast.Ellipsis:
		return "..." + typeExprString(t.Elt)
	case *ast.IndexExpr:
		// Generic type with single type parameter, e.g., Foo[T].
		return typeExprString(t.X) + "[" + typeExprString(t.Index) + "]"
	case *ast.IndexListExpr:
		// Generic type with multiple type parameters, e.g., Foo[T, U].
		var params []string
		for _, idx := range t.Indices {
			params = append(params, typeExprString(idx))
		}
		return typeExprString(t.X) + "[" + strings.Join(params, ", ") + "]"
	default:
		return fmt.Sprintf("<%T>", expr)
	}
}

// fieldListString converts a field list to a comma-separated string of "name type" pairs.
func fieldListString(fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, field := range fl.List {
		typeStr := typeExprString(field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, typeStr)
		} else {
			for _, name := range field.Names {
				parts = append(parts, name.Name+" "+typeStr)
			}
		}
	}
	return strings.Join(parts, ", ")
}

// hashString computes a SHA-256 hash of a string and returns the first 16 hex characters.
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}

// hashStrings computes a combined hash from multiple hash strings.
func hashStrings(hashes []string) string {
	return hashString(strings.Join(hashes, ":"))
}
