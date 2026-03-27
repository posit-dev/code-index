# code-index

Semantic code search for AI coding assistants. Parses source code, generates LLM summaries, and builds a searchable vector index exposed via MCP.

## Integrating code-index into a project

When asked to set up code-index for a repository, follow these steps:

### 1. Create `.code-index.json`

Create this file in the repository root. Identify all source directories and their languages:

```json
{
  "project": "<repo-name>",
  "sources": [
    {"path": "src", "language": "go", "exclude": ["**/vendor/**", "**/*_test.go", "**/testdata/**"]},
    {"path": "frontend/src", "language": "typescript", "exclude": ["**/*.test.ts", "**/__tests__/**"]}
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
  "storage": {
    "s3_bucket": "<bucket-name>",
    "s3_prefix": "vectors"
  },
  "aws": {
    "region": "us-east-1",
    "account": "<aws-account-id>",
    "profiles": ["team", "production"]
  }
}
```

Supported languages: `go`, `typescript`, `javascript`, `python`, `r`, `c`, `cpp`, `markdown`.

For Go projects with vendored dependencies, add `vendor_include` to index specific vendor modules:
```json
{"path": "src", "language": "go", "vendor_include": ["github.com/myorg/shared-lib"]}
```

### 2. Add MCP server to `.mcp.json`

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

### 3. Add `.code-index/` to `.gitignore`

```
# Code index — generated data distributed via S3
.code-index/
```

### 4. Create CI workflow at `.github/workflows/code-index.yml`

See `docs/ci-setup.md` for the full workflow template. Key requirements:
- `permissions: contents: read` and `id-token: write`
- Install `libsqlite3-dev` on Linux before `go install`
- Pull existing data from S3 before running the pipeline
- Upload results back to S3 after the pipeline

### 5. Document in CLAUDE.md

Add `code_search` to the project's MCP tools table and describe usage:

```markdown
| `code_search` | Semantic search across the codebase (via code-index MCP server) |
```

### What NOT to do

- Don't install the CLI locally for developers — the MCP server handles search automatically
- Don't create a pull script — the MCP server downloads from S3 on first search
- Don't commit `.code-index/` to git — it's distributed via S3

## Developing code-index itself

See `docs/` for full documentation:
- `docs/getting-started.md` — setup walkthrough
- `docs/configuration.md` — config reference
- `docs/aws-setup.md` — Bedrock and S3 permissions
- `docs/ci-setup.md` — GitHub Actions workflow

### Building and testing

```bash
go build ./cmd/code-index/        # Build CLI
go test ./...                      # Run tests
cd mcp && npx tsc --noEmit        # TypeScript check
```

### Release process

Push a tag to trigger binary releases and npm publish:
```bash
git tag v0.X.Y && git push origin v0.X.Y
```
