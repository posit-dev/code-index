// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Generator handles LLM-based doc generation with caching.
type Generator struct {
	outputDir       string
	config          *IndexConfig
	backend         LLMBackend
	dryRun          bool
	verbose         bool
	backendOverride string
	// maxFiles limits the number of files to process (0 = unlimited).
	maxFiles int
}

// GeneratorOption configures a Generator.
type GeneratorOption func(*Generator)

// WithMaxFiles limits the number of files to process.
func WithMaxFiles(n int) GeneratorOption {
	return func(g *Generator) { g.maxFiles = n }
}

// WithVerbose enables verbose logging of LLM calls.
func WithVerbose(v bool) GeneratorOption {
	return func(g *Generator) { g.verbose = v }
}

// WithBackendOverride forces a specific LLM backend.
func WithBackendOverride(backend string) GeneratorOption {
	return func(g *Generator) { g.backendOverride = backend }
}

// NewGenerator creates a new doc generator.
// Config provides model IDs and AWS settings. Use opts to override behavior.
func NewGenerator(outputDir string, config *IndexConfig, dryRun bool, opts ...GeneratorOption) (*Generator, error) {
	g := &Generator{
		outputDir: outputDir,
		config:    config,
		dryRun:    dryRun,
	}
	for _, opt := range opts {
		opt(g)
	}

	if !dryRun {
		provider := g.backendOverride
		if provider == "" {
			provider = config.LLM.Provider
		}
		backend, err := selectBackend(provider, g.verbose, config.AWS.Region)
		if err != nil {
			return nil, err
		}
		g.backend = backend
		fmt.Fprintf(os.Stderr, "Using LLM backend: %s\n", backend.Name())
	}

	return g, nil
}

// selectBackend chooses the LLM backend based on provider name.
func selectBackend(provider string, verbose bool, awsRegion string) (LLMBackend, error) {
	switch provider {
	case "cli":
		return NewCLIBackend(verbose)
	case "bedrock":
		return NewBedrockLLMBackend(awsRegion)
	case "":
		// Auto-detect: prefer Bedrock, then CLI.
		bedrock, err := NewBedrockLLMBackend(awsRegion)
		if err == nil {
			return bedrock, nil
		}
		cli, cliErr := NewCLIBackend(verbose)
		if cliErr == nil {
			return cli, nil
		}
		return nil, fmt.Errorf("no LLM backend available:\n  Bedrock: %v\n  Claude Code CLI: %v", err, cliErr)
	default:
		return nil, fmt.Errorf("unknown backend %q (use \"bedrock\", \"cli\", or \"\" for auto-detect)", provider)
	}
}

// GenerateStats tracks generation statistics.
type GenerateStats struct {
	FunctionsGenerated int
	FilesGenerated     int
	PackagesGenerated  int
	FunctionsSkipped   int
	FilesSkipped       int
	PackagesSkipped    int
	FunctionsRemoved   int
	FilesRemoved       int
	PackagesRemoved    int
}

// Generate produces LLM summaries for all changed items in the diff.
// It updates the cache manifest and writes doc files.
func (g *Generator) Generate(parsed *ParseResult, diff *DiffResult, cache *CacheManifest) (*GenerateStats, error) {
	stats := &GenerateStats{}
	var mu sync.Mutex // protects cache and stats

	docsDir := filepath.Join(g.outputDir, CacheDir)
	for _, sub := range []string{"func", "file", "pkg"} {
		if err := os.MkdirAll(filepath.Join(docsDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("creating docs directory: %w", err)
		}
	}

	const numWorkers = 20

	// saveCounter tracks writes since last cache save.
	saveCounter := 0
	maybeSaveCache := func() {
		saveCounter++
		if saveCounter%10 == 0 {
			if err := SaveCacheManifest(g.outputDir, cache); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: failed to save cache: %v\n", err)
			}
		}
	}

	// Phase 1: Generate function docs (Haiku) — parallel.
	fileGroups := groupFunctionsByFile(diff.ChangedFunctions)
	totalFileGroups := len(fileGroups)
	if g.maxFiles > 0 && totalFileGroups > g.maxFiles {
		totalFileGroups = g.maxFiles
	}
	fmt.Fprintf(os.Stderr, "\nPhase 1/3: Function docs (Haiku) — %d file batches, %d workers\n", totalFileGroups, numWorkers)

	type funcWorkItem struct {
		filePath string
		fileInfo *FileInfo
		funcs    []*FunctionInfo
	}

	// Collect work items.
	var funcWork []funcWorkItem
	for filePath, funcs := range fileGroups {
		if g.maxFiles > 0 && len(funcWork) >= g.maxFiles {
			stats.FunctionsSkipped += len(funcs)
			continue
		}
		fileInfo := parsed.Files[filePath]
		if fileInfo == nil {
			continue
		}
		funcWork = append(funcWork, funcWorkItem{filePath, fileInfo, funcs})
	}

	if !g.dryRun {
		var wg sync.WaitGroup
		workCh := make(chan int, len(funcWork))
		errCh := make(chan error, numWorkers)

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range workCh {
					item := funcWork[idx]
					chunks := chunkFunctions(item.funcs, 30)
					for _, chunk := range chunks {
						docs, err := g.generateFunctionDocs(item.fileInfo, chunk)
						if err != nil {
							errCh <- fmt.Errorf("generating function docs for %s: %w", item.filePath, err)
							return
						}
						mu.Lock()
						for key, doc := range docs {
							fn := diff.ChangedFunctions[key]
							if fn == nil {
								mu.Unlock()
								// skip silently
								mu.Lock()
								continue
							}
							if wErr := writeDoc(funcDocPath(docsDir, key), doc); wErr != nil {
								fmt.Fprintf(os.Stderr, "\n  warning: failed to write doc for %s: %v", key, wErr)
							}
							cache.Functions[key] = &FunctionCache{
								ASTHash:       fn.ASTHash,
								SigHash:       fn.SigHash,
								DocHash:       hashString(doc),
								LastGenerated: time.Now(),
							}
							stats.FunctionsGenerated++
						}
						maybeSaveCache()
						mu.Unlock()
					}
				}
			}()
		}

		// Progress reporter.
		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					mu.Lock()
					n := stats.FunctionsGenerated
					mu.Unlock()
					fmt.Fprintf(os.Stderr, "  [%d functions generated...]\n", n)
				case <-done:
					return
				}
			}
		}()

		for i := range funcWork {
			workCh <- i
		}
		close(workCh)
		wg.Wait()
		close(done)

		select {
		case err := <-errCh:
			return nil, err
		default:
		}

		if err := SaveCacheManifest(g.outputDir, cache); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: failed to save cache: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "  Done: %d function summaries generated\n", stats.FunctionsGenerated)
	} else {
		for _, item := range funcWork {
			stats.FunctionsGenerated += len(item.funcs)
		}
		fmt.Fprintf(os.Stderr, "  Skipped (dry-run): %d functions\n", stats.FunctionsGenerated)
	}

	// Phase 2: Generate file docs (Sonnet) — parallel.
	type fileWorkItem struct {
		filePath string
		fileInfo *FileInfo
	}
	var fileWork []fileWorkItem
	for filePath, fileInfo := range diff.ChangedFiles {
		if g.maxFiles > 0 && len(fileWork) >= g.maxFiles {
			stats.FilesSkipped++
			continue
		}
		fileWork = append(fileWork, fileWorkItem{filePath, fileInfo})
	}

	totalFiles := len(fileWork)
	fmt.Fprintf(os.Stderr, "\nPhase 2/3: File docs (Sonnet) — %d files, %d workers\n", totalFiles, numWorkers)

	if !g.dryRun {
		var wg sync.WaitGroup
		workCh := make(chan int, len(fileWork))
		errCh := make(chan error, numWorkers)

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range workCh {
					item := fileWork[idx]
					doc, err := g.generateFileDoc(item.fileInfo, parsed)
					if err != nil {
						errCh <- fmt.Errorf("generating file doc for %s: %w", item.filePath, err)
						return
					}
					if wErr := writeDoc(fileDocPath(docsDir, item.filePath), doc); wErr != nil {
						fmt.Fprintf(os.Stderr, "  warning: failed to write file doc for %s: %v\n", item.filePath, wErr)
					}

					var hashes []string
					for i := range item.fileInfo.Functions {
						hashes = append(hashes, item.fileInfo.Functions[i].ASTHash)
					}
					for i := range item.fileInfo.Types {
						hashes = append(hashes, item.fileInfo.Types[i].ASTHash)
					}

					mu.Lock()
					cache.Files[item.filePath] = &FileCache{
						FuncDocHash:   hashStrings(hashes),
						LastGenerated: time.Now(),
					}
					stats.FilesGenerated++
					maybeSaveCache()
					mu.Unlock()
				}
			}()
		}

		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					mu.Lock()
					n := stats.FilesGenerated
					mu.Unlock()
					fmt.Fprintf(os.Stderr, "  [%d/%d files generated...]\n", n, totalFiles)
				case <-done:
					return
				}
			}
		}()

		for i := range fileWork {
			workCh <- i
		}
		close(workCh)
		wg.Wait()
		close(done)

		select {
		case err := <-errCh:
			return nil, err
		default:
		}

		if err := SaveCacheManifest(g.outputDir, cache); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: failed to save cache: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "  Done: %d file docs generated\n", stats.FilesGenerated)
	} else {
		stats.FilesGenerated = len(fileWork)
		fmt.Fprintf(os.Stderr, "  Skipped (dry-run): %d files\n", stats.FilesGenerated)
	}

	// Phase 3: Generate package docs (Sonnet) — parallel.
	type pkgWorkItem struct {
		importPath string
		pkgInfo    *PackageInfo
	}
	var pkgWork []pkgWorkItem
	for importPath, pkgInfo := range diff.ChangedPackages {
		if g.maxFiles > 0 && len(pkgWork) >= g.maxFiles {
			stats.PackagesSkipped++
			continue
		}
		pkgWork = append(pkgWork, pkgWorkItem{importPath, pkgInfo})
	}

	totalPkgs := len(pkgWork)
	fmt.Fprintf(os.Stderr, "\nPhase 3/3: Package docs (Sonnet) — %d packages, %d workers\n", totalPkgs, numWorkers)

	if !g.dryRun {
		var wg sync.WaitGroup
		workCh := make(chan int, len(pkgWork))
		errCh := make(chan error, numWorkers)

		for w := 0; w < numWorkers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range workCh {
					item := pkgWork[idx]
					doc, err := g.generatePackageDoc(item.pkgInfo, parsed)
					if err != nil {
						errCh <- fmt.Errorf("generating package doc for %s: %w", item.importPath, err)
						return
					}
					if wErr := writeDoc(pkgDocPath(docsDir, item.importPath), doc); wErr != nil {
						fmt.Fprintf(os.Stderr, "  warning: failed to write package doc for %s: %v\n", item.importPath, wErr)
					}

					var fileHashes []string
					for _, fp := range item.pkgInfo.Files {
						if fi, ok := parsed.Files[fp]; ok {
							fileHashes = append(fileHashes, fi.ASTHash)
						}
					}

					mu.Lock()
					cache.Packages[item.importPath] = &PackageCache{
						FileDocHash:   hashStrings(fileHashes),
						LastGenerated: time.Now(),
					}
					stats.PackagesGenerated++
					maybeSaveCache()
					mu.Unlock()
				}
			}()
		}

		done := make(chan struct{})
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					mu.Lock()
					n := stats.PackagesGenerated
					mu.Unlock()
					fmt.Fprintf(os.Stderr, "  [%d/%d packages generated...]\n", n, totalPkgs)
				case <-done:
					return
				}
			}
		}()

		for i := range pkgWork {
			workCh <- i
		}
		close(workCh)
		wg.Wait()
		close(done)

		select {
		case err := <-errCh:
			return nil, err
		default:
		}

		if err := SaveCacheManifest(g.outputDir, cache); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: failed to save cache: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "  Done: %d package docs generated\n", stats.PackagesGenerated)
	} else {
		stats.PackagesGenerated = len(pkgWork)
		fmt.Fprintf(os.Stderr, "  Skipped (dry-run): %d packages\n", stats.PackagesGenerated)
	}

	// Clean up removed entries.
	for _, key := range diff.RemovedFunctions {
		os.Remove(funcDocPath(docsDir, key))
		delete(cache.Functions, key)
		stats.FunctionsRemoved++
	}
	for _, key := range diff.RemovedFiles {
		os.Remove(fileDocPath(docsDir, key))
		delete(cache.Files, key)
		stats.FilesRemoved++
	}
	for _, key := range diff.RemovedPackages {
		os.Remove(pkgDocPath(docsDir, key))
		delete(cache.Packages, key)
		stats.PackagesRemoved++
	}

	return stats, nil
}

// detectLanguage infers the programming language from a file path.
func detectLanguage(path string) string {
	switch {
	case strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx") ||
		strings.HasSuffix(path, ".vue") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".jsx"):
		return "TypeScript/JavaScript"
	case strings.HasSuffix(path, ".py"):
		return "Python"
	case strings.HasSuffix(path, ".R") || strings.HasSuffix(path, ".r"):
		return "R"
	case strings.HasSuffix(path, ".c") || strings.HasSuffix(path, ".h"):
		return "C"
	case strings.HasSuffix(path, ".cpp") || strings.HasSuffix(path, ".hpp") || strings.HasSuffix(path, ".cc"):
		return "C/C++"
	case strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".qmd"):
		return "Markdown"
	default:
		return "Go"
	}
}

// generateFunctionDocs generates summaries for a batch of functions from the same file.
func (g *Generator) generateFunctionDocs(fileInfo *FileInfo, funcs []*FunctionInfo) (map[string]string, error) {
	lang := detectLanguage(fileInfo.Path)

	var prompt strings.Builder
	fmt.Fprintf(&prompt, "You are a %s code documentation assistant. Generate a concise one-line summary for each function below. ", lang)
	prompt.WriteString("The summary should describe what the function does, its key behavior, and when you'd use it. ")
	prompt.WriteString("Be specific — mention parameter types, return values, and edge cases when relevant. ")
	prompt.WriteString("Do NOT start with the function name. ")
	prompt.WriteString("Respond in JSON format: {\"summaries\": {\"key\": \"summary\", ...}}\n\n")
	fmt.Fprintf(&prompt, "File: %s (package %s)\n\n", fileInfo.Path, fileInfo.Package)

	for _, fn := range funcs {
		key := FunctionCacheKey(fn.File, fn.Name, fn.Receiver)
		fmt.Fprintf(&prompt, "Key: %s\n", key)
		fmt.Fprintf(&prompt, "Signature: %s\n", fn.Signature)
		if fn.Doc != "" {
			fmt.Fprintf(&prompt, "Doc: %s\n", fn.Doc)
		}
		prompt.WriteString("\n")
	}

	// Build a lookup from various short forms to full keys so we can remap
	// LLM responses that use abbreviated keys.
	shortToFull := make(map[string]string)
	for _, fn := range funcs {
		key := FunctionCacheKey(fn.File, fn.Name, fn.Receiver)
		// "FuncName"
		shortToFull[fn.Name] = key
		if fn.Receiver != "" {
			// "*Type.FuncName" or "Type.FuncName"
			shortToFull[fn.Receiver+"."+fn.Name] = key
			// "(*Type).FuncName"
			recv := fn.Receiver
			if strings.HasPrefix(recv, "*") {
				shortToFull["("+recv+")."+fn.Name] = key
			}
		}
	}

	resp, err := g.backend.Call(g.config.FunctionModel(), prompt.String())
	if err != nil {
		return nil, err
	}

	// Parse the JSON response. The LLM may wrap it in markdown code fences
	// or include extra text before/after the JSON.
	resp = stripCodeFences(resp)

	summaries, err := parseSummariesResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("parsing LLM response: %w (response: %s)", err, resp)
	}

	// Remap abbreviated keys to full cache keys.
	remapped := make(map[string]string, len(summaries))
	for key, summary := range summaries {
		if fullKey, ok := shortToFull[key]; ok {
			remapped[fullKey] = summary
		} else {
			remapped[key] = summary
		}
	}

	return remapped, nil
}

// generateFileDoc generates a summary for a file using its function summaries.
func (g *Generator) generateFileDoc(fileInfo *FileInfo, parsed *ParseResult) (string, error) {
	lang := detectLanguage(fileInfo.Path)

	var prompt strings.Builder
	fmt.Fprintf(&prompt, "You are a %s code documentation assistant. Generate a 2-3 sentence summary of this file. ", lang)
	prompt.WriteString("Explain what the file does, why it exists, and what role it plays in the package. ")
	prompt.WriteString("Be specific about the domain. Respond with ONLY the summary text, no formatting.\n\n")
	fmt.Fprintf(&prompt, "File: %s\nPackage: %s (%s)\n\n", fileInfo.Path, fileInfo.Package, fileInfo.ImportPath)

	if fileInfo.Doc != "" {
		fmt.Fprintf(&prompt, "File doc comment: %s\n\n", fileInfo.Doc)
	}

	prompt.WriteString("Functions:\n")
	for _, fn := range fileInfo.Functions {
		key := FunctionCacheKey(fn.File, fn.Name, fn.Receiver)
		// Try to read the cached function doc.
		doc := readDocFile(filepath.Join(g.outputDir, CacheDir, "func"), key)
		if doc == "" {
			doc = fn.Doc
		}
		fmt.Fprintf(&prompt, "  %s — %s\n", fn.Signature, doc)
	}

	if len(fileInfo.Types) > 0 {
		prompt.WriteString("\nTypes:\n")
		for _, t := range fileInfo.Types {
			fmt.Fprintf(&prompt, "  %s %s — %s\n", t.Kind, t.Name, t.Doc)
		}
	}

	return g.backend.Call(g.config.SummaryModel(), prompt.String())
}

// generatePackageDoc generates a summary for a package using its file summaries.
func (g *Generator) generatePackageDoc(pkgInfo *PackageInfo, parsed *ParseResult) (string, error) {
	var prompt strings.Builder
	// Detect language from the first file in the package.
	pkgLang := "Go"
	if len(pkgInfo.Files) > 0 {
		pkgLang = detectLanguage(pkgInfo.Files[0])
	}
	fmt.Fprintf(&prompt, "You are a %s code documentation assistant. Generate a 2-4 sentence summary of this package. ", pkgLang)
	prompt.WriteString("Explain what the package does, its role in the system architecture, and what other packages would use it for. ")
	prompt.WriteString("Be specific about the domain. Respond with ONLY the summary text, no formatting.\n\n")
	fmt.Fprintf(&prompt, "Package: %s (import path: %s)\n\n", filepath.Base(pkgInfo.Dir), pkgInfo.ImportPath)

	if pkgInfo.Doc != "" {
		fmt.Fprintf(&prompt, "Package doc: %s\n\n", pkgInfo.Doc)
	}

	prompt.WriteString("Files:\n")
	for _, filePath := range pkgInfo.Files {
		doc := readDocFile(filepath.Join(g.outputDir, CacheDir, "file"), filePath)
		if doc == "" {
			if fi := parsed.Files[filePath]; fi != nil {
				doc = fi.Doc
			}
		}
		fmt.Fprintf(&prompt, "  %s — %s\n", filepath.Base(filePath), doc)
	}

	// List exported symbols.
	prompt.WriteString("\nExported symbols:\n")
	for _, filePath := range pkgInfo.Files {
		fi := parsed.Files[filePath]
		if fi == nil {
			continue
		}
		for _, fn := range fi.Functions {
			if fn.Exported {
				fmt.Fprintf(&prompt, "  %s\n", fn.Signature)
			}
		}
		for _, t := range fi.Types {
			if t.Exported {
				fmt.Fprintf(&prompt, "  type %s (%s)\n", t.Name, t.Kind)
			}
		}
	}

	return g.backend.Call(g.config.SummaryModel(), prompt.String())
}

// groupFunctionsByFile groups changed functions by their file path.
func groupFunctionsByFile(funcs map[string]*FunctionInfo) map[string][]*FunctionInfo {
	groups := make(map[string][]*FunctionInfo)
	for _, fn := range funcs {
		groups[fn.File] = append(groups[fn.File], fn)
	}
	return groups
}

// chunkFunctions splits a slice of functions into chunks of at most size n.
func chunkFunctions(funcs []*FunctionInfo, n int) [][]*FunctionInfo {
	if len(funcs) <= n {
		return [][]*FunctionInfo{funcs}
	}
	var chunks [][]*FunctionInfo
	for i := 0; i < len(funcs); i += n {
		end := i + n
		if end > len(funcs) {
			end = len(funcs)
		}
		chunks = append(chunks, funcs[i:end])
	}
	return chunks
}

// funcDocPath returns the file path for a function's doc.
// All doc paths are lowercased to avoid case-sensitivity issues on macOS.
func funcDocPath(docsDir, key string) string {
	safe := strings.ReplaceAll(key, "/", "_")
	safe = strings.ReplaceAll(safe, "::", "__")
	safe = strings.ReplaceAll(safe, ".", "_")
	return filepath.Join(docsDir, "func", strings.ToLower(safe)+".md")
}

// fileDocPath returns the file path for a file's doc.
func fileDocPath(docsDir, filePath string) string {
	safe := strings.ReplaceAll(filePath, "/", "_")
	return filepath.Join(docsDir, "file", strings.ToLower(safe)+".md")
}

// pkgDocPath returns the file path for a package's doc.
func pkgDocPath(docsDir, importPath string) string {
	safe := strings.ReplaceAll(importPath, "/", "_")
	return filepath.Join(docsDir, "pkg", strings.ToLower(safe)+".md")
}

// writeDoc writes a doc string to a file.
func writeDoc(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// readDocFile reads a cached doc file for functions/packages, returning empty string if not found.
// Uses the full sanitization (replacing /, ::, and .) matching funcDocPath and pkgDocPath.
func readDocFile(dir, key string) string {
	safe := strings.ReplaceAll(key, "/", "_")
	safe = strings.ReplaceAll(safe, "::", "__")
	safe = strings.ReplaceAll(safe, ".", "_")
	path := filepath.Join(dir, strings.ToLower(safe)+".md")
	return readDocFileByPath(path)
}

// readDocFileByPath reads a doc file by its exact path, returning empty string if not found.
func readDocFileByPath(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// parseSummariesResponse extracts a map of key→summary strings from an LLM response.
// Handles code fences, nested objects, and other LLM quirks.
func parseSummariesResponse(resp string) (map[string]string, error) {
	resp = stripCodeFences(resp)

	// First try strict parsing.
	var strict struct {
		Summaries map[string]string `json:"summaries"`
	}
	if err := json.Unmarshal([]byte(resp), &strict); err == nil {
		return strict.Summaries, nil
	}

	// Try extracting the first JSON object.
	extracted := extractFirstJSON(resp)
	if extracted == "" {
		return nil, fmt.Errorf("no JSON found in response")
	}

	// Try strict parsing on extracted JSON.
	if err := json.Unmarshal([]byte(extracted), &strict); err == nil {
		return strict.Summaries, nil
	}

	// Lenient parsing: unmarshal values as interface{} and keep only strings.
	// This handles cases where the LLM nests an extra "summaries": {} object
	// or includes non-string values.
	var lenient struct {
		Summaries map[string]interface{} `json:"summaries"`
	}
	if err := json.Unmarshal([]byte(extracted), &lenient); err != nil {
		return nil, err
	}

	result := make(map[string]string, len(lenient.Summaries))
	for k, v := range lenient.Summaries {
		if s, ok := v.(string); ok {
			result[k] = s
		}
		// Skip non-string values (like nested "summaries": {})
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no valid string summaries found")
	}

	return result, nil
}

// stripCodeFences removes markdown code fences (```json ... ```) from a response.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	// Remove leading ```json or ```
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
	}
	// Remove trailing ```
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// extractFirstJSON extracts the first complete JSON object from a string.
// Handles cases where the LLM outputs multiple JSON blocks or extra text.
func extractFirstJSON(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	// Walk forward counting braces to find the matching close.
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		if escape {
			escape = false
			continue
		}
		c := s[i]
		if c == '\\' && inString {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
