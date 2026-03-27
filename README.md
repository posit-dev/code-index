# code-index

Semantic code search for AI coding assistants. Parses your source code, generates LLM-powered summaries, and builds a searchable vector index exposed via [MCP](https://modelcontextprotocol.io/).

## What it does

code-index lets AI assistants like [Claude Code](https://claude.com/claude-code) search your codebase by *concept* instead of exact keywords:

```
"check if string is in slice"     → finds StringInSlice in util.go
"how does authentication work"    → finds auth packages, token handlers, middleware
"database transaction management" → finds BeginTransaction, CommitTransaction across packages
```

It works by generating natural language summaries of every function, file, and package, then embedding them as vectors for similarity search.

## Supported languages

| Language | Parser |
|----------|--------|
| Go | Native `go/ast` |
| TypeScript / Vue | tree-sitter |
| Python | tree-sitter |
| C / C++ | tree-sitter |
| R | Native via Rscript (regex fallback) |
| Markdown / Quarto | Regex |

## Quick start

### 1. Install

Download a pre-built binary from [Releases](https://github.com/posit-dev/code-index/releases), or install from source:

```bash
go install github.com/posit-dev/code-index/cmd/code-index@latest
```

### 2. Configure

Create `.code-index.json` in your repository root (see [Configuration](docs/configuration.md)):

```json
{
  "project": "my-project",
  "sources": [
    {"path": "src", "language": "go"},
    {"path": "frontend", "language": "typescript"}
  ],
  "llm": {
    "provider": "openai",
    "base_url": "http://localhost:11434/v1",
    "function_model": "llama3.2",
    "summary_model": "llama3.2"
  },
  "embeddings": {
    "provider": "openai",
    "base_url": "http://localhost:11434/v1",
    "model": "nomic-embed-text"
  }
}
```

This example uses [Ollama](https://ollama.com) for fully local operation. You can also use [OpenAI](docs/getting-started.md#openai), [AWS Bedrock](docs/getting-started.md#aws-bedrock-best-quality), or any OpenAI-compatible API.

### 3. Build the index

```bash
code-index all    # parse → generate → build → embed
```

### 4. Add to Claude Code

Add the MCP server to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "code-index": {
      "command": "npx",
      "args": ["@posit-dev/code-index-mcp"]
    }
  }
}
```

Claude Code will use `code_search` proactively when working in your codebase.

## How it works

```
parse → generate → build → embed → search
 AST     LLM docs   JSON    vectors   query
```

1. **Parse** — extracts functions, types, classes from source files using language-specific parsers
2. **Generate** — creates LLM summaries for every function (fast model) and file/package (quality model) via AWS Bedrock
3. **Build** — combines AST data and summaries into a searchable index
4. **Embed** — generates vector embeddings, stored in a SQLite database with [sqlite-vec](https://github.com/asg017/sqlite-vec)
5. **Search** — embeds your query, finds the closest vectors, returns results with signatures, summaries, and source locations

## Documentation

- [Getting Started](docs/getting-started.md) — detailed setup walkthrough
- [Configuration](docs/configuration.md) — full config reference with examples
- [AWS Setup](docs/aws-setup.md) — Bedrock access, IAM roles, credential chain
- [CI Setup](docs/ci-setup.md) — nightly index updates via GitHub Actions

## License

MIT — see [LICENSE](LICENSE).
