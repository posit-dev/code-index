// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/cpp"
)

// CPPParser extracts structured information from C++ source files using tree-sitter.
type CPPParser struct {
	srcRoot  string
	excludes []string
	lang     *sitter.Language
}

// NewCPPParser creates a new C++ parser.
func NewCPPParser(srcRoot string, excludes []string) *CPPParser {
	return &CPPParser{
		srcRoot:  srcRoot,
		excludes: excludes,
		lang:     cpp.GetLanguage(),
	}
}

// Parse walks the source tree and extracts all file, function, and type information.
func (p *CPPParser) Parse(result *ParseResult) error {
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
		if ext != ".cpp" && ext != ".cc" && ext != ".hpp" && ext != ".cxx" && ext != ".hxx" {
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

func (p *CPPParser) parseFile(path, relPath string, result *ParseResult) error {
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

	// Extract declarations with namespace tracking.
	ctx := &cppParseContext{
		content:    content,
		filePath:   fileInfo.Path,
		namespaces: nil,
	}
	p.extractDeclarations(root, ctx, fileInfo)

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

// cppParseContext tracks parsing state like namespace nesting and template parameters.
type cppParseContext struct {
	content    []byte
	filePath   string
	namespaces []string // Current namespace stack (e.g., ["rstudio", "core"]).
	template   string   // Current template parameters (e.g., "template<typename T>").
}

// qualifiedName returns the fully qualified name given current namespace context.
func (ctx *cppParseContext) qualifiedName(name string) string {
	if len(ctx.namespaces) == 0 {
		return name
	}
	return strings.Join(ctx.namespaces, "::") + "::" + name
}

// pushNamespace adds a namespace to the stack.
func (ctx *cppParseContext) pushNamespace(ns string) {
	ctx.namespaces = append(ctx.namespaces, ns)
}

// popNamespace removes the last namespace from the stack.
func (ctx *cppParseContext) popNamespace() {
	if len(ctx.namespaces) > 0 {
		ctx.namespaces = ctx.namespaces[:len(ctx.namespaces)-1]
	}
}

func (p *CPPParser) extractDeclarations(node *sitter.Node, ctx *cppParseContext, fileInfo *FileInfo) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		nodeType := child.Type()

		switch nodeType {
		case "function_definition":
			if fn := p.extractFunction(child, ctx, ""); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}

		case "declaration":
			// Function declarations (prototypes) in headers.
			if fn := p.extractFunctionDeclaration(child, ctx, ""); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}

		case "struct_specifier", "union_specifier":
			if t := p.extractStructOrUnion(child, ctx); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "enum_specifier":
			if t := p.extractEnum(child, ctx); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "type_definition":
			if t := p.extractTypedef(child, ctx); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "class_specifier":
			if t := p.extractClass(child, ctx); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}

		case "namespace_definition":
			p.extractNamespace(child, ctx, fileInfo)

		case "template_declaration":
			p.extractTemplate(child, ctx, fileInfo)

		// Preprocessor blocks - recurse into their bodies to find declarations
		// inside #ifndef/#ifdef/#if guards (common in header files).
		case "preproc_ifdef", "preproc_if", "preproc_else", "preproc_elif":
			p.extractDeclarations(child, ctx, fileInfo)
		}
	}
}

// extractNamespace handles namespace definitions and recurses into their body.
func (p *CPPParser) extractNamespace(node *sitter.Node, ctx *cppParseContext, fileInfo *FileInfo) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		// Anonymous namespace - still recurse.
		if body := node.ChildByFieldName("body"); body != nil {
			p.extractDeclarations(body, ctx, fileInfo)
		}
		return
	}

	nsName := nameNode.Content(ctx.content)
	ctx.pushNamespace(nsName)
	defer ctx.popNamespace()

	if body := node.ChildByFieldName("body"); body != nil {
		p.extractDeclarations(body, ctx, fileInfo)
	}
}

// extractTemplate handles template declarations (classes, functions, structs).
func (p *CPPParser) extractTemplate(node *sitter.Node, ctx *cppParseContext, fileInfo *FileInfo) {
	// Extract template parameters.
	templateParams := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "template_parameter_list" {
			templateParams = "template" + child.Content(ctx.content)
			break
		}
	}

	oldTemplate := ctx.template
	ctx.template = templateParams
	defer func() { ctx.template = oldTemplate }()

	// Find the actual declaration inside the template.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "class_specifier":
			if t := p.extractClass(child, ctx); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}
		case "struct_specifier":
			if t := p.extractStructOrUnion(child, ctx); t != nil {
				fileInfo.Types = append(fileInfo.Types, *t)
			}
		case "function_definition":
			if fn := p.extractFunction(child, ctx, ""); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}
		case "declaration":
			if fn := p.extractFunctionDeclaration(child, ctx, ""); fn != nil {
				fileInfo.Functions = append(fileInfo.Functions, *fn)
			}
		}
	}
}

func (p *CPPParser) extractFunction(node *sitter.Node, ctx *cppParseContext, receiver string) *FunctionInfo {
	declarator := node.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}

	name, qualifiedReceiver := extractCPPNameWithQualifier(declarator, ctx.content)
	if name == "" {
		return nil
	}

	// If the declarator has a qualified name (e.g., Foo::bar), use that as receiver.
	if qualifiedReceiver != "" {
		receiver = qualifiedReceiver
	}

	sig := p.buildSignature(node, ctx)
	doc := extractPrecedingComment(node, ctx.content)
	line := int(node.StartPoint().Row) + 1

	qualName := name
	if receiver == "" && len(ctx.namespaces) > 0 {
		qualName = ctx.qualifiedName(name)
	}

	returns, calls := extractBodyInfo(node, ctx.content)

	return &FunctionInfo{
		Name:      qualName,
		Receiver:  receiver,
		Signature: sig,
		Doc:       doc,
		File:      ctx.filePath,
		Line:      line,
		Exported:  true,
		ASTHash:   hashString(node.Content(ctx.content)),
		SigHash:   hashString(sig),
		Returns:   returns,
		Calls:     calls,
	}
}

func (p *CPPParser) extractFunctionDeclaration(
	node *sitter.Node,
	ctx *cppParseContext,
	receiver string,
) *FunctionInfo {
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

	name, qualifiedReceiver := extractCPPNameWithQualifier(declarator, ctx.content)
	if name == "" {
		return nil
	}

	if qualifiedReceiver != "" {
		receiver = qualifiedReceiver
	}

	sig := p.buildDeclarationSignature(node, ctx)
	doc := extractPrecedingComment(node, ctx.content)
	line := int(node.StartPoint().Row) + 1

	qualName := name
	if receiver == "" && len(ctx.namespaces) > 0 {
		qualName = ctx.qualifiedName(name)
	}

	return &FunctionInfo{
		Name:      qualName,
		Receiver:  receiver,
		Signature: sig,
		Doc:       doc,
		File:      ctx.filePath,
		Line:      line,
		Exported:  true,
		ASTHash:   hashString(node.Content(ctx.content)),
		SigHash:   hashString(sig),
	}
}

func (p *CPPParser) extractStructOrUnion(node *sitter.Node, ctx *cppParseContext) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil // Anonymous struct/union.
	}

	name := nameNode.Content(ctx.content)
	kind := "struct"
	if node.Type() == "union_specifier" {
		kind = "union"
	}
	doc := extractPrecedingComment(node, ctx.content)

	qualName := name
	if len(ctx.namespaces) > 0 {
		qualName = ctx.qualifiedName(name)
	}

	typeInfo := &TypeInfo{
		Name:      qualName,
		Kind:      kind,
		Signature: ctx.template,
		Doc:       doc,
		File:      ctx.filePath,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
		ASTHash:   hashString(node.Content(ctx.content)),
	}

	// Extract fields and methods from struct body.
	if body := node.ChildByFieldName("body"); body != nil {
		p.extractClassMembers(body, ctx, typeInfo, name)
	}

	return typeInfo
}

func (p *CPPParser) extractEnum(node *sitter.Node, ctx *cppParseContext) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(ctx.content)
	doc := extractPrecedingComment(node, ctx.content)

	qualName := name
	if len(ctx.namespaces) > 0 {
		qualName = ctx.qualifiedName(name)
	}

	return &TypeInfo{
		Name:     qualName,
		Kind:     "enum",
		Doc:      doc,
		File:     ctx.filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: true,
		ASTHash:  hashString(node.Content(ctx.content)),
	}
}

func (p *CPPParser) extractTypedef(node *sitter.Node, ctx *cppParseContext) *TypeInfo {
	declarator := node.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}

	name, _ := extractCPPNameWithQualifier(declarator, ctx.content)
	if name == "" {
		return nil
	}

	doc := extractPrecedingComment(node, ctx.content)

	qualName := name
	if len(ctx.namespaces) > 0 {
		qualName = ctx.qualifiedName(name)
	}

	return &TypeInfo{
		Name:     qualName,
		Kind:     "typedef",
		Doc:      doc,
		File:     ctx.filePath,
		Line:     int(node.StartPoint().Row) + 1,
		Exported: true,
		ASTHash:  hashString(node.Content(ctx.content)),
	}
}

func (p *CPPParser) extractClass(node *sitter.Node, ctx *cppParseContext) *TypeInfo {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}

	name := nameNode.Content(ctx.content)
	doc := extractPrecedingComment(node, ctx.content)

	qualName := name
	if len(ctx.namespaces) > 0 {
		qualName = ctx.qualifiedName(name)
	}

	typeInfo := &TypeInfo{
		Name:      qualName,
		Kind:      "class",
		Signature: ctx.template,
		Doc:       doc,
		File:      ctx.filePath,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
		ASTHash:   hashString(node.Content(ctx.content)),
	}

	// Extract methods and fields from class body.
	if body := node.ChildByFieldName("body"); body != nil {
		p.extractClassMembers(body, ctx, typeInfo, name)
	}

	return typeInfo
}

// extractClassMembers extracts methods and fields from a class/struct body.
func (p *CPPParser) extractClassMembers(
	body *sitter.Node,
	ctx *cppParseContext,
	typeInfo *TypeInfo,
	className string,
) {
	currentAccess := "private"
	if typeInfo.Kind == "struct" {
		currentAccess = "public"
	}

	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		nodeType := child.Type()

		switch nodeType {
		case "access_specifier":
			// Update access level (public, private, protected).
			for j := 0; j < int(child.ChildCount()); j++ {
				c := child.Child(j)
				switch c.Type() {
				case "public", "private", "protected":
					currentAccess = c.Type()
				}
			}

		case "field_declaration":
			// Check if this is a method declaration or a field.
			if method := p.extractMethodFromField(child, ctx, className, currentAccess); method != nil {
				typeInfo.Methods = append(typeInfo.Methods, *method)
			} else if field := p.extractFieldFromDecl(child, ctx); field != nil {
				typeInfo.Fields = append(typeInfo.Fields, *field)
			}

		case "function_definition":
			// Inline method definition.
			if method := p.extractInlineMethod(child, ctx, className, currentAccess); method != nil {
				typeInfo.Methods = append(typeInfo.Methods, *method)
			}

		case "declaration":
			// Constructor/destructor declarations.
			if method := p.extractConstructorDecl(child, ctx, className, currentAccess); method != nil {
				typeInfo.Methods = append(typeInfo.Methods, *method)
			}

		case "template_declaration":
			// Template method inside class.
			p.extractTemplateMethod(child, ctx, typeInfo, className, currentAccess)
		}
	}
}

// extractMethodFromField extracts a method declaration from a field_declaration.
func (p *CPPParser) extractMethodFromField(
	node *sitter.Node,
	ctx *cppParseContext,
	className string,
	access string,
) *FunctionInfo {
	// Look for function_declarator to identify this as a method.
	var funcDecl *sitter.Node
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "function_declarator" {
			funcDecl = child
			break
		}
	}
	if funcDecl == nil {
		return nil
	}

	name := ""
	for i := 0; i < int(funcDecl.ChildCount()); i++ {
		child := funcDecl.Child(i)
		if child.Type() == "field_identifier" || child.Type() == "identifier" {
			name = child.Content(ctx.content)
			break
		}
	}
	if name == "" {
		return nil
	}

	sig := p.buildFieldDeclSignature(node, ctx)
	doc := extractPrecedingComment(node, ctx.content)

	return &FunctionInfo{
		Name:      name,
		Receiver:  className,
		Signature: sig,
		Doc:       doc,
		File:      ctx.filePath,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  access == "public",
		ASTHash:   hashString(node.Content(ctx.content)),
		SigHash:   hashString(sig),
	}
}

// extractInlineMethod extracts an inline method definition.
func (p *CPPParser) extractInlineMethod(
	node *sitter.Node,
	ctx *cppParseContext,
	className string,
	access string,
) *FunctionInfo {
	declarator := node.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}

	name := ""
	for declarator != nil {
		switch declarator.Type() {
		case "function_declarator":
			for i := 0; i < int(declarator.ChildCount()); i++ {
				child := declarator.Child(i)
				if child.Type() == "field_identifier" || child.Type() == "identifier" {
					name = child.Content(ctx.content)
					break
				}
			}
			if name != "" {
				break
			}
			declarator = declarator.ChildByFieldName("declarator")
		case "field_identifier", "identifier":
			name = declarator.Content(ctx.content)
			declarator = nil
		default:
			declarator = declarator.ChildByFieldName("declarator")
		}
		if name != "" {
			break
		}
	}

	if name == "" {
		return nil
	}

	sig := p.buildSignature(node, ctx)
	doc := extractPrecedingComment(node, ctx.content)
	returns, calls := extractBodyInfo(node, ctx.content)

	return &FunctionInfo{
		Name:      name,
		Receiver:  className,
		Signature: sig,
		Doc:       doc,
		File:      ctx.filePath,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  access == "public",
		ASTHash:   hashString(node.Content(ctx.content)),
		SigHash:   hashString(sig),
		Returns:   returns,
		Calls:     calls,
	}
}

// extractConstructorDecl extracts constructor/destructor declarations.
func (p *CPPParser) extractConstructorDecl(
	node *sitter.Node,
	ctx *cppParseContext,
	className string,
	access string,
) *FunctionInfo {
	declarator := node.ChildByFieldName("declarator")
	if declarator == nil {
		return nil
	}

	// Check for function_declarator.
	if declarator.Type() != "function_declarator" {
		return nil
	}

	name := ""
	for i := 0; i < int(declarator.ChildCount()); i++ {
		child := declarator.Child(i)
		switch child.Type() {
		case "identifier":
			name = child.Content(ctx.content)
		case "destructor_name":
			// ~ClassName.
			for j := 0; j < int(child.ChildCount()); j++ {
				if child.Child(j).Type() == "identifier" {
					name = "~" + child.Child(j).Content(ctx.content)
					break
				}
			}
		}
		if name != "" {
			break
		}
	}

	if name == "" {
		return nil
	}

	sig := truncateSignature(strings.TrimSpace(strings.TrimSuffix(node.Content(ctx.content), ";")))
	doc := extractPrecedingComment(node, ctx.content)

	return &FunctionInfo{
		Name:      name,
		Receiver:  className,
		Signature: sig,
		Doc:       doc,
		File:      ctx.filePath,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  access == "public",
		ASTHash:   hashString(node.Content(ctx.content)),
		SigHash:   hashString(sig),
	}
}

// extractTemplateMethod extracts template methods inside a class.
func (p *CPPParser) extractTemplateMethod(
	node *sitter.Node,
	ctx *cppParseContext,
	typeInfo *TypeInfo,
	className string,
	access string,
) {
	// Get template parameters.
	templateParams := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "template_parameter_list" {
			templateParams = "template" + child.Content(ctx.content)
			break
		}
	}

	// Find the function inside.
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "function_definition":
			if method := p.extractInlineMethod(child, ctx, className, access); method != nil {
				method.Signature = templateParams + " " + method.Signature
				typeInfo.Methods = append(typeInfo.Methods, *method)
			}
		case "field_declaration":
			if method := p.extractMethodFromField(child, ctx, className, access); method != nil {
				method.Signature = templateParams + " " + method.Signature
				typeInfo.Methods = append(typeInfo.Methods, *method)
			}
		}
	}
}

// extractFieldFromDecl extracts a field (non-method) from a field_declaration.
func (p *CPPParser) extractFieldFromDecl(node *sitter.Node, ctx *cppParseContext) *FieldInfo {
	// If it has a function_declarator, it's a method, not a field.
	for i := 0; i < int(node.ChildCount()); i++ {
		if node.Child(i).Type() == "function_declarator" {
			return nil
		}
	}

	var typeName string
	var fieldName string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "primitive_type", "type_identifier", "qualified_identifier":
			typeName = child.Content(ctx.content)
		case "field_identifier":
			fieldName = child.Content(ctx.content)
		}
	}

	if fieldName == "" {
		return nil
	}

	doc := extractPrecedingComment(node, ctx.content)

	return &FieldInfo{
		Name: fieldName,
		Type: typeName,
		Doc:  doc,
	}
}

// buildSignature builds a function signature from a function_definition.
func (p *CPPParser) buildSignature(node *sitter.Node, ctx *cppParseContext) string {
	// Take everything before the function body.
	body := node.ChildByFieldName("body")
	var sig string
	if body != nil {
		sig = strings.TrimSpace(string(ctx.content[node.StartByte():body.StartByte()]))
	} else {
		// Fallback: first line.
		text := node.Content(ctx.content)
		if idx := strings.Index(text, "{"); idx > 0 {
			sig = strings.TrimSpace(text[:idx])
		} else {
			sig = text
		}
	}

	// Prepend template if present.
	if ctx.template != "" {
		sig = ctx.template + " " + sig
	}

	return truncateSignature(sig)
}

// buildDeclarationSignature builds a signature from a declaration (prototype).
func (p *CPPParser) buildDeclarationSignature(node *sitter.Node, ctx *cppParseContext) string {
	sig := strings.TrimSpace(strings.TrimSuffix(node.Content(ctx.content), ";"))

	if ctx.template != "" {
		sig = ctx.template + " " + sig
	}

	return truncateSignature(sig)
}

// buildFieldDeclSignature builds a signature from a field_declaration (method decl).
func (p *CPPParser) buildFieldDeclSignature(node *sitter.Node, ctx *cppParseContext) string {
	return truncateSignature(strings.TrimSpace(strings.TrimSuffix(node.Content(ctx.content), ";")))
}


// extractCPPNameWithQualifier extracts the name and any class qualifier from a declarator.
// For "Foo::bar", returns ("bar", "Foo"). For "bar", returns ("bar", "").
func extractCPPNameWithQualifier(declarator *sitter.Node, content []byte) (string, string) {
	for declarator != nil {
		switch declarator.Type() {
		case "identifier", "field_identifier", "type_identifier":
			return declarator.Content(content), ""

		case "qualified_identifier":
			// Handle Foo::bar or rstudio::core::Foo::bar.
			return extractCPPQualifiedName(declarator, content)

		case "function_declarator", "pointer_declarator", "array_declarator",
			"parenthesized_declarator":
			declarator = declarator.ChildByFieldName("declarator")
			if declarator == nil {
				return "", ""
			}

		default:
			// Try to find an identifier child.
			for i := 0; i < int(declarator.ChildCount()); i++ {
				child := declarator.Child(i)
				switch child.Type() {
				case "identifier", "field_identifier", "type_identifier":
					return child.Content(content), ""
				case "qualified_identifier":
					return extractCPPQualifiedName(child, content)
				}
			}
			return "", ""
		}
	}
	return "", ""
}

// extractCPPQualifiedName handles qualified identifiers like Foo::bar or ns::Foo::bar.
// Returns (name, receiver). For member functions, receiver is the full qualifier
// (for example, ns::Foo). For namespace-qualified free functions, the full
// qualified name is returned in name and receiver is empty.
func extractCPPQualifiedName(node *sitter.Node, content []byte) (string, string) {
	var parts []cppQualifiedPart
	collectCPPQualifiedParts(node, content, &parts)

	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0].text, ""
	}

	name := parts[len(parts)-1].text
	qualifierParts := parts[:len(parts)-1]
	lastQualifier := qualifierParts[len(qualifierParts)-1]

	qualifierTexts := make([]string, 0, len(qualifierParts))
	for _, part := range qualifierParts {
		qualifierTexts = append(qualifierTexts, part.text)
	}

	switch lastQualifier.kind {
	case "namespace_identifier":
		// Namespace-qualified free function: keep the full qualifier in the name.
		return strings.Join(append(qualifierTexts, name), "::"), ""
	case "type_identifier", "template_type", "identifier":
		// Member function: keep the full qualifier chain as the receiver.
		return name, strings.Join(qualifierTexts, "::")
	default:
		// Fall back to preserving the full qualifier in the name.
		return strings.Join(append(qualifierTexts, name), "::"), ""
	}
}

type cppQualifiedPart struct {
	text string
	kind string
}

// collectCPPQualifiedParts recursively collects parts of a qualified identifier
// along with their syntactic kinds so namespaces can be distinguished from types.
func collectCPPQualifiedParts(node *sitter.Node, content []byte, parts *[]cppQualifiedPart) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "namespace_identifier", "identifier", "type_identifier":
			*parts = append(*parts, cppQualifiedPart{
				text: child.Content(content),
				kind: child.Type(),
			})
		case "template_type":
			// Handle Container<T> by recording the template type name as a type-like qualifier.
			for j := 0; j < int(child.ChildCount()); j++ {
				grandchild := child.Child(j)
				if grandchild.Type() == "type_identifier" {
					*parts = append(*parts, cppQualifiedPart{
						text: grandchild.Content(content),
						kind: "template_type",
					})
					break
				}
			}
		case "qualified_identifier":
			collectCPPQualifiedParts(child, content, parts)
		}
	}
}
