// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	treesitterc "github.com/smacker/go-tree-sitter/c"
)

// CParser extracts structured information from C source files using tree-sitter.
type CParser struct {
	srcRoot  string
	excludes []string
	cLang    *sitter.Language
}

// NewCParser creates a new C parser.
func NewCParser(srcRoot string, excludes []string) *CParser {
	return &CParser{
		srcRoot:  srcRoot,
		excludes: excludes,
		cLang:    treesitterc.GetLanguage(),
	}
}

// Parse walks the source tree and extracts all file, function, and type information.
func (p *CParser) Parse(result *ParseResult) error {
	return filepath.Walk(p.srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == "build" || base == "CMakeFiles" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if ext != ".c" && ext != ".h" {
			return nil
		}

		relPath, err := filepath.Rel(p.srcRoot, path)
		if err != nil {
			return nil
		}

		for _, pattern := range p.excludes {
			if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
				return nil
			}
			if matched, err := filepath.Match(pattern, filepath.Base(path)); err == nil && matched {
				return nil
			}
		}

		return p.parseFile(path, relPath, result)
	})
}

func (p *CParser) parseFile(path, relPath string, result *ParseResult) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(p.cLang)

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", relPath, err)
		return nil
	}
	defer tree.Close()

	root := tree.RootNode()

	dir := filepath.Dir(relPath)
	importPath := filepath.ToSlash(dir)

	fileInfo := &FileInfo{
		Path:       filepath.ToSlash(relPath),
		Package:    filepath.Base(dir),
		ImportPath: importPath,
	}

	p.extractDeclarations(root, content, fileInfo)

	fileInfo.ASTHash = hashString(string(content))

	if len(fileInfo.Functions) > 0 || len(fileInfo.Types) > 0 {
		result.Files[fileInfo.Path] = fileInfo

		if _, ok := result.Packages[importPath]; !ok {
			result.Packages[importPath] = &PackageInfo{
				ImportPath: importPath,
				Dir:        dir,
			}
		}
		result.Packages[importPath].Files = append(result.Packages[importPath].Files, fileInfo.Path)
	}

	return nil
}

func (p *CParser) extractDeclarations(node *sitter.Node, content []byte, fileInfo *FileInfo) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		nodeType := child.Type()

		switch nodeType {
		case "function_definition":
			if fn := p.extractFunction(child, content, fileInfo.Path); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}

		case "declaration":
			// Function declarations (prototypes) in headers.
			if fn := p.extractFunctionDeclaration(child, content, fileInfo.Path); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}

		case "struct_specifier", "union_specifier":
			if t := p.extractStructOrUnion(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "enum_specifier":
			if t := p.extractEnum(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "type_definition":
			if t := p.extractTypedef(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		// Preprocessor blocks — recurse into their bodies to find declarations
		// inside #ifndef/#ifdef/#if guards (common in header files).
		case "preproc_ifdef", "preproc_if", "preproc_else", "preproc_elif":
			p.extractDeclarations(child, content, fileInfo)
		}
	}
}

func (p *CParser) extractFunction(node *sitter.Node, content []byte, filePath string) *FunctionInfo {
	declarator := node.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}

	name := extractCName(declarator, content)
	if name == "" {
		return nil
	}

	sig := extractCSignature(node, content)
	doc := extractPrecedingComment(node, content)
	line := int(node.StartPoint().Row) + 1

	return &FunctionInfo{
		Name:      name,
		Signature: sig,
		Doc:       doc,
		File:      filePath,
		Line:      line,
		Exported:  true, // C doesn't have export visibility at syntax level
		ASTHash:   hashString(node.Content(content)),
		SigHash:   hashString(sig),
	}
}

func (p *CParser) extractFunctionDeclaration(node *sitter.Node, content []byte, filePath string) *FunctionInfo {
	declarator := node.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}

	// Only match function declarators (with parameter list).
	if declarator.Type() != "function_declarator" {
		// Check for pointer_declarator wrapping function_declarator.
		found := false
		for j := 0; j < int(declarator.ChildCount()); j++ {
			if declarator.Child(j).Type() == "function_declarator" {
				declarator = declarator.Child(j)
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}

	name := extractCName(declarator, content)
	if name == "" {
		return nil
	}

	sig := strings.TrimSpace(strings.TrimSuffix(node.Content(content), ";"))
	if len(sig) > 200 {
		sig = sig[:200] + "..."
	}
	doc := extractPrecedingComment(node, content)
	line := int(node.StartPoint().Row) + 1

	return &FunctionInfo{
		Name:      name,
		Signature: sig,
		Doc:       doc,
		File:      filePath,
		Line:      line,
		Exported:  true,
		ASTHash:   hashString(node.Content(content)),
		SigHash:   hashString(sig),
	}
}

func (p *CParser) extractStructOrUnion(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil // Anonymous struct/union
	}

	name := nameNode.Content(content)
	kind := "struct"
	if node.Type() == "union_specifier" {
		kind = "union"
	}
	doc := extractPrecedingComment(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     kind,
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: true,
		ASTHash:  hashString(node.Content(content)),
	}
}

func (p *CParser) extractEnum(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	doc := extractPrecedingComment(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     "enum",
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: true,
		ASTHash:  hashString(node.Content(content)),
	}
}

func (p *CParser) extractTypedef(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	// Get the typedef name (last identifier before the semicolon).
	declarator := node.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}

	name := extractCName(declarator, content)
	if name == "" {
		return nil
	}

	doc := extractPrecedingComment(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     "typedef",
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: true,
		ASTHash:  hashString(node.Content(content)),
	}
}

// extractCName finds the function/variable name from a declarator node.
func extractCName(declarator *sitter.Node, content []byte) string {
	// Walk down through pointer_declarator, function_declarator, etc.
	for declarator != nil {
		switch declarator.Type() {
		case "identifier", "field_identifier", "type_identifier":
			return declarator.Content(content)
		case "function_declarator", "pointer_declarator", "array_declarator", "parenthesized_declarator":
			declarator = declarator.ChildByFieldName("declarator")
			if declarator == nil {
				// Try first child as fallback
				return ""
			}
		default:
			// Try to find an identifier child
			for i := 0; i < int(declarator.ChildCount()); i++ {
				child := declarator.Child(i)
				if child.Type() == "identifier" || child.Type() == "field_identifier" || child.Type() == "type_identifier" {
					return child.Content(content)
				}
			}
			return ""
		}
	}
	return ""
}

// extractCSignature gets the signature from a function definition.
func extractCSignature(node *sitter.Node, content []byte) string {
	// Take everything before the function body.
	body := node.ChildByFieldName("body")
	if body != nil {
		return truncateSignature(strings.TrimSpace(string(content[node.StartByte():body.StartByte()])))
	}
	// Fallback: first line.
	text := node.Content(content)
	if idx := strings.Index(text, "{"); idx > 0 {
		text = strings.TrimSpace(text[:idx])
	}
	return truncateSignature(text)
}
