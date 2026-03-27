# @jonyoder/code-index-mcp

MCP server for semantic code search using vector embeddings. Pairs with the [code-index](https://github.com/posit-dev/code-index) CLI tool.

## What it does

Exposes a `code_search` tool that AI coding assistants (like [Claude Code](https://claude.com/claude-code)) use to search your codebase by concept:

```
"check if string is in slice"     → finds StringInSlice in util.go
"how does authentication work"    → finds auth packages, token handlers
"database transaction management" → finds BeginTransaction across packages
```

## Setup

### 1. Build the index

Install the [code-index CLI](https://github.com/posit-dev/code-index) and run:

```bash
code-index all
```

This parses your source code, generates LLM summaries, and builds a searchable vector database in `.code-index/`.

### 2. Add the MCP server

Add to your project's `.mcp.json`:

```json
{
  "mcpServers": {
    "code-index": {
      "command": "npx",
      "args": ["-y", "@jonyoder/code-index-mcp"]
    }
  }
}
```

Claude Code will use `code_search` proactively when working in your codebase.

## How it works

On each search query, the MCP server:

1. Finds the `.code-index/code-index.db` database (searches upward from cwd)
2. Embeds the query using the configured provider (Bedrock, OpenAI, or Ollama)
3. Searches the SQLite vector database for the closest matches
4. Returns results with function signatures, file locations, and summaries

If configured, it also checks for updated databases in the background (via S3 or HTTP URL) so your team always has current search results.

## Configuration

The server reads `.code-index.json` from your repository root for embedding provider settings. See the [configuration docs](https://github.com/posit-dev/code-index/blob/main/docs/configuration.md) for details.

## Requirements

- **Node.js 20+**
- A built code-index database (`.code-index/code-index.db`), or storage configured in `.code-index.json` for automatic download from S3/HTTP
- An embedding provider configured (AWS Bedrock, OpenAI, or Ollama)
- For S3 storage: AWS credentials (the server auto-detects profiles from `aws.profiles` in `.code-index.json`)

## License

MIT — see [LICENSE](https://github.com/posit-dev/code-index/blob/main/LICENSE).
