// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

// PythonParser extracts structured information from Python source files using tree-sitter.
type PythonParser struct {
	srcRoot  string
	excludes []string
	lang     *sitter.Language
}

// NewPythonParser creates a new Python parser.
func NewPythonParser(srcRoot string, excludes []string) *PythonParser {
	return &PythonParser{
		srcRoot:  srcRoot,
		excludes: excludes,
		lang:     python.GetLanguage(),
	}
}

// Parse walks the source tree and extracts all file, function, and type information.
func (p *PythonParser) Parse(result *ParseResult) error {
	return filepath.Walk(p.srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == "__pycache__" || base == ".venv" || base == "venv" ||
				base == "node_modules" || base == ".tox" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".py") {
			return nil
		}

		relPath, err := filepath.Rel(p.srcRoot, path)
		if err != nil {
			return nil
		}

		// Check excludes.
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

func (p *PythonParser) parseFile(path, relPath string, result *ParseResult) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(p.lang)

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

	// Extract module-level docstring.
	if root.ChildCount() > 0 {
		first := root.Child(0)
		if first.Type() == "expression_statement" && first.ChildCount() > 0 {
			expr := first.Child(0)
			if expr.Type() == "string" {
				fileInfo.Doc = cleanPythonDocstring(expr.Content(content))
			}
		}
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

func (p *PythonParser) extractDeclarations(node *sitter.Node, content []byte, fileInfo *FileInfo) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		nodeType := child.Type()

		switch nodeType {
		case "function_definition":
			if fn := p.extractFunction(child, content, fileInfo.Path); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}

		case "class_definition":
			if t := p.extractClass(child, content, fileInfo.Path); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
				// Extract methods from the class body.
				p.extractMethods(child, content, fileInfo)
			}

		case "decorated_definition":
			// Unwrap the decorator to get the actual definition.
			for j := 0; j < int(child.ChildCount()); j++ {
				inner := child.Child(j)
				switch inner.Type() {
				case "function_definition":
					if fn := p.extractFunction(inner, content, fileInfo.Path); fn != nil {
						fn.Doc = extractPythonDocOrDecorator(child, inner, content)
						fn.Line = int(child.StartPoint().Row) + 1 // Use decorator line
						fileInfo.Functions = append(fileInfo.Functions, *fn)
					}
				case "class_definition":
					if t := p.extractClass(inner, content, fileInfo.Path); t != nil {
						t.Line = int(child.StartPoint().Row) + 1
						fileInfo.Types = append(fileInfo.Types, *t)
						p.extractMethods(inner, content, fileInfo)
					}
				}
			}
		}
	}
}

func (p *PythonParser) extractFunction(node *sitter.Node, content []byte, filePath string) *FunctionInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	line := int(node.StartPoint().Row) + 1
	sig := extractPythonSignature(node, content)
	doc := extractPythonDocstring(node, content)

	return &FunctionInfo{
		Name:      name,
		Signature: sig,
		Doc:       doc,
		File:      filePath,
		Line:      line,
		Exported:  !strings.HasPrefix(name, "_"),
		ASTHash:   hashString(node.Content(content)),
		SigHash:   hashString(sig),
	}
}

func (p *PythonParser) extractClass(node *sitter.Node, content []byte, filePath string) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(content)
	doc := extractPythonDocstring(node, content)

	return &TypeInfo{
		Name:     name,
		Kind:     "class",
		Doc:      doc,
		File:     filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: !strings.HasPrefix(name, "_"),
		ASTHash:  hashString(node.Content(content)),
	}
}

func (p *PythonParser) extractMethods(classNode *sitter.Node, content []byte, fileInfo *FileInfo) {
	body := classNode.ChildByFieldName("body")
	if body == nil {
		return
	}

	className := ""
	nameNode := classNode.ChildByFieldName("name")
	if nameNode != nil {
		className = nameNode.Content(content)
	}

	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)

		var funcNode *sitter.Node
		switch child.Type() {
		case "function_definition":
			funcNode = child
		case "decorated_definition":
			for j := 0; j < int(child.ChildCount()); j++ {
				if child.Child(j).Type() == "function_definition" {
					funcNode = child.Child(j)
					break
				}
			}
		}

		if funcNode == nil {
			continue
		}

		fn := p.extractFunction(funcNode, content, fileInfo.Path)
		if fn == nil {
			continue
		}

		// Skip dunder methods except __init__
		if strings.HasPrefix(fn.Name, "__") && fn.Name != "__init__" {
			continue
		}

		fn.Receiver = className
		fn.Signature = fmt.Sprintf("def %s.%s%s", className, fn.Name, extractPythonParams(funcNode, content))
		fileInfo.Functions = append(fileInfo.Functions, *fn)
	}
}

// extractPythonSignature builds a signature like "def func_name(param1, param2) -> ReturnType"
func extractPythonSignature(node *sitter.Node, content []byte) string {
	name := ""
	if n := node.ChildByFieldName("name"); n != nil {
		name = n.Content(content)
	}

	params := extractPythonParams(node, content)

	returnType := ""
	if rt := node.ChildByFieldName("return_type"); rt != nil {
		returnType = " -> " + rt.Content(content)
	}

	return fmt.Sprintf("def %s%s%s", name, params, returnType)
}

func extractPythonParams(node *sitter.Node, content []byte) string {
	if params := node.ChildByFieldName("parameters"); params != nil {
		return params.Content(content)
	}
	return "()"
}

// extractPythonDocstring extracts the docstring from a function or class body.
func extractPythonDocstring(node *sitter.Node, content []byte) string {
	body := node.ChildByFieldName("body")
	if body == nil || body.ChildCount() == 0 {
		return ""
	}

	first := body.Child(0)
	if first.Type() == "expression_statement" && first.ChildCount() > 0 {
		expr := first.Child(0)
		if expr.Type() == "string" {
			return cleanPythonDocstring(expr.Content(content))
		}
	}
	return ""
}

// extractPythonDocOrDecorator extracts docstring, falling back to decorator comment.
func extractPythonDocOrDecorator(decoratedNode, innerNode *sitter.Node, content []byte) string {
	// Try docstring first.
	if doc := extractPythonDocstring(innerNode, content); doc != "" {
		return doc
	}
	// Try comment before the decorator.
	return extractPrecedingComment(decoratedNode, content)
}

// cleanPythonDocstring strips triple quotes and cleans up a docstring.
func cleanPythonDocstring(raw string) string {
	// Remove triple quotes.
	for _, q := range []string{`"""`, `'''`} {
		raw = strings.TrimPrefix(raw, q)
		raw = strings.TrimSuffix(raw, q)
	}

	// Clean up each line.
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	result := strings.Join(lines, " ")

	// Truncate very long docstrings.
	if len(result) > 300 {
		result = result[:300] + "..."
	}
	return result
}
