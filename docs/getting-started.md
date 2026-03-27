# Getting Started

This guide walks you through setting up code-index for your project.

## Prerequisites

- **Go 1.21+** — for building the CLI tool
- **Node.js 20+** — for the MCP search server
- **AWS account** with Bedrock access — for LLM summaries and embeddings
- **Claude Code** (optional) — the MCP tool integrates with Claude Code, but the CLI works standalone

### Optional

- **R** — for parsing R source files (falls back to regex if unavailable)
- **S3 bucket** — for distributing the vector database across a team

## Installation

### CLI tool

```bash
go install github.com/posit-dev/code-index/cmd/code-index@latest
```

Or build from source:

```bash
git clone https://github.com/posit-dev/code-index.git
cd code-index
go build -o code-index ./cmd/code-index/
```

### MCP server

No separate installation needed — Claude Code runs it via `npx`:

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

## Creating your configuration

Create `.code-index.json` in your repository root. Start with the example:

```bash
cp .code-index.example.json .code-index.json
```

Edit it to match your project structure. At minimum, you need:

```json
{
  "sources": [
    {"path": "src", "language": "go"}
  ],
  "llm": {
    "provider": "bedrock",
    "function_model": "us.anthropic.claude-haiku-4-5-20251001-v1:0",
    "summary_model": "us.anthropic.claude-sonnet-4-6"
  },
  "embeddings": {
    "provider": "bedrock",
    "model": "cohere.embed-v4:0"
  },
  "aws": {
    "region": "us-east-1"
  }
}
```

See [Configuration](configuration.md) for the full reference.

## Building the index

### Step by step

```bash
# 1. Parse source files (fast, no network calls)
code-index parse

# 2. Generate LLM summaries (requires Bedrock)
code-index generate

# 3. Build the searchable JSON index
code-index build

# 4. Create vector embeddings (requires Bedrock)
code-index embed
```

### All at once

```bash
code-index all
```

### Partial builds

For large codebases, you can limit how many items are processed:

```bash
code-index generate --limit 20    # Generate 20 file batches
code-index embed --limit 100      # Embed 100 items
```

Run again without `--limit` to continue. The cache ensures already-done items are skipped.

## Searching

### From the CLI

```bash
code-index search "check if string is in slice"
code-index search "how does authentication work"
code-index search --max-results 20 "database transaction management"
```

### From Claude Code

Once the MCP server is configured in `.mcp.json`, Claude Code uses `code_search` proactively. You can also ask Claude directly:

> "Use code_search to find how authentication works in this project."

### JSON output

For programmatic use:

```bash
code-index search --json "authentication"
```

## Next steps

- [Configuration reference](configuration.md) — all config options
- [AWS Setup](aws-setup.md) — Bedrock access and credentials
- [CI Setup](ci-setup.md) — automate nightly index updates
