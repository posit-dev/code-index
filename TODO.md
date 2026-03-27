# TODO

Items to convert to GitHub issues once the repo is created.

## MCP Server Enhancements

### Auto-update vector database

The MCP tool should automatically check for and pull updated vector databases
without requiring manual script runs.

**Storage providers:**

The tool supports two download providers, auto-detected from config:

1. **S3** — for AWS-heavy orgs. Uses AWS SDK credential chain (SSO, IAM roles).
   ```json
   {"storage": {"s3_bucket": "my-index", "s3_prefix": "vectors"}}
   ```

2. **HTTP URL** — universal, works with any hosting (GitHub Releases, GCS,
   Azure Blob, Artifactory, CDNs, internal servers).
   ```json
   {"storage": {"url": "https://example.com/code-index/latest.tar.gz"}}
   ```
   For authenticated endpoints, set `auth_token_env` to the env var name
   containing a bearer token:
   ```json
   {"storage": {"url": "https://github.com/org/repo/releases/download/code-index/latest.tar.gz", "auth_token_env": "GITHUB_TOKEN"}}
   ```

The SHA URL is derived automatically (`{url}.sha256`). Auto-detect: if `url`
is set, use HTTP; if `s3_bucket` is set, use S3.

**Upload is the user's responsibility.** The tool only handles downloading.
CI docs provide copy-paste examples for common platforms (S3, GitHub Releases,
GCS, Azure Blob).

**Proposed auto-update behavior:**
1. On each search, check `.vectors-sha256` file age
2. If older than 1 hour, fetch the SHA file (one small GET)
3. If SHA differs from local, download new DB in the background
4. Current search uses existing DB immediately (never blocks)
5. Next search uses the updated DB

**Conflict safety:**
- Downloads go to a temp file, then atomic rename — no partial reads
- `better-sqlite3` opens files with shared locks; replacing the file while
  another session reads it is safe on both Linux and macOS (old inode stays
  valid until the reader closes)
- At most one background download at a time (use a lock file)
- SQLite is opened readonly by the MCP tool, so no WAL conflicts

**SHA check frequency:** Cache the remote SHA for 1 hour to avoid excessive
calls. This means developers get updates within ~1 hour of CI pushing
new data, without any manual intervention.

### Publish as npm package

Publish `@jonyoder/code-index-mcp` to npm so any project can use it with:
```json
{
  "mcpServers": {
    "code-index": {
      "command": "npx",
      "args": ["@jonyoder/code-index-mcp"]
    }
  }
}
```

Requires `prepublishOnly` build step (already configured in package.json).

## Language Parsers

### ~~Improve R parser with auto-download of R runtime~~ (DEFERRED)

Auto-download is impractical — cdn.posit.co serves platform-specific
installers (.pkg, .deb, .rpm), not portable binaries. Instead, the parser
now shows a clear install hint when Rscript is not found, and the docs
cover installation for each platform. The regex fallback works well for
most R code.

### Add more languages

Potential languages to add based on demand:
- **Rust** — tree-sitter grammar available in `smacker/go-tree-sitter`
- **Java** — tree-sitter grammar available
- **Ruby** — tree-sitter grammar available
- **Shell/Bash** — regex-based, extract functions
- **SQL** — regex-based, extract stored procedures/views

Each language requires ~200-300 lines: a parser file + language detection
in `generate.go` and `parse.go`.

## Testing

### Expand test coverage

Current tests cover:
- JSON response parsing (stripCodeFences, extractFirstJSON, parseSummariesResponse)
- Config loading and defaults
- Markdown parser (sections, front matter, code block stripping)

Needs tests for:
- Go AST parser (function extraction, vendor-aware indexing)
- TypeScript parser (exports, arrow functions, Vue SFCs)
- Python parser (classes, decorators, docstrings)
- C/C++ parser (headers, preprocessor guards, namespaces)
- R parser (native mode, regex fallback)
- Vector store (add, search, upsert, reset)
- Embed cache (incremental updates, hash matching)
- Cache manifest (diff computation, invalidation)

### Add integration test

Create an integration test that:
1. Indexes the `testdata/` fixtures
2. Builds the index (without LLM — use mock summaries)
3. Embeds with a mock embedder (deterministic vectors)
4. Searches and verifies results

This would validate the full pipeline without requiring AWS credentials.

## CI / Release

### Create GitHub Action

Publish a reusable GitHub Action (`posit-dev/code-index-action`) so any
repo can add nightly index updates:

```yaml
- uses: posit-dev/code-index-action@v1
  with:
    config: .code-index.json
```

The action should:
- Install the `code-index` binary
- Pull existing data from S3
- Run the full pipeline (parse → generate → build → embed)
- Upload updated data to S3
- Optionally create a PR with updated checked-in files

### Release binaries

Build and publish pre-compiled binaries for Linux, macOS (arm64 + amd64),
and Windows via GitHub Releases. This avoids requiring Go to be installed
for the CLI tool.

Consider using GoReleaser for automated cross-compilation.

## Architecture

### ~~Add OpenAI-compatible provider for LLM and embeddings~~ (DONE)

The OpenAI chat/completions and embeddings API format has become the de facto
standard. Ollama, OpenAI, Together AI, Groq, Fireworks, LM Studio, Azure
OpenAI, and vLLM all expose it. Adding a single `openai` provider covers all
of them and removes the AWS requirement for getting started.

**Provider matrix after this change:**

| Provider | LLM | Embeddings | Auth |
|----------|-----|------------|------|
| `bedrock` | Claude via Bedrock | Cohere via Bedrock | AWS IAM |
| `openai` | Any OpenAI-compatible | Any OpenAI-compatible | API key or none (Ollama) |

**Remove the `cli` backend.** It spawns a new `claude` process per LLM call,
which is far too slow for the `generate` step (thousands of calls). Anyone
who used `cli` to avoid AWS setup can use `openai` pointed at Ollama instead.

**Config examples:**

Ollama (fully local, no API key, no cloud account):
```json
{
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

OpenAI:
```json
{
  "llm": {
    "provider": "openai",
    "api_key_env": "OPENAI_API_KEY",
    "function_model": "gpt-4o-mini",
    "summary_model": "gpt-4o"
  },
  "embeddings": {
    "provider": "openai",
    "api_key_env": "OPENAI_API_KEY",
    "model": "text-embedding-3-small"
  }
}
```

Mixed providers (Bedrock for LLM quality, OpenAI for embeddings):
```json
{
  "llm": {
    "provider": "bedrock",
    "function_model": "us.anthropic.claude-haiku-4-5-20251001-v1:0",
    "summary_model": "us.anthropic.claude-sonnet-4-6"
  },
  "embeddings": {
    "provider": "openai",
    "api_key_env": "OPENAI_API_KEY",
    "model": "text-embedding-3-small"
  }
}
```

Note: Anthropic's API does not implement the OpenAI chat/completions format,
so Claude cannot be used via the `openai` provider. Use `bedrock` for Claude.

**Implementation — Go side:**

1. `OpenAILLMBackend` (~60 lines) — implements `LLMBackend`. Uses `net/http`
   directly; the API is simple enough that no SDK is needed:
   ```
   POST {base_url}/chat/completions
   Authorization: Bearer {api_key}
   {"model": "...", "messages": [{"role": "user", "content": "..."}]}
   ```

2. `OpenAIEmbedder` (~60 lines) — implements `Embedder`. Calls:
   ```
   POST {base_url}/embeddings
   Authorization: Bearer {api_key}
   {"model": "...", "input": "text to embed"}
   ```

3. No new Go dependencies required.

**Implementation — TypeScript MCP side:**

The MCP server's `embedQuery()` function needs the same provider switch so
that search queries are embedded with the same model used at index time.
Use `fetch()` against the OpenAI-compatible endpoint — no SDK needed.

**Config schema changes:**

Add `base_url` and `api_key_env` fields to both `llm` and `embeddings`:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `base_url` | string | `https://api.openai.com/v1` | API base URL |
| `api_key_env` | string | `OPENAI_API_KEY` | Env var name containing the API key |

Use `api_key_env` (env var name) rather than `api_key` (raw secret) so that
config files can be safely committed to the repo.

**Dynamic embedding dimensions:**

The `EmbeddingDimensions` constant (currently hardcoded to 1536 for Cohere)
must become dynamic. Different models produce different dimensions:

| Model | Dimensions |
|-------|-----------|
| Cohere Embed v4 | 1536 |
| OpenAI text-embedding-3-small | 1536 |
| OpenAI text-embedding-3-large | 3072 |
| nomic-embed-text (Ollama) | 768 |
| mxbai-embed-large (Ollama) | 1024 |
| all-minilm (Ollama) | 384 |

Approach: detect dimensions from the first embedding response and store in
the database metadata. The `vec_items` table creation uses the dimension at
schema init time, so this must be known before inserting. On `--reset` or
first build, read from the response. On subsequent runs, read from a stored
value in the database (e.g., a `metadata` table with key/value pairs).

If the stored dimension doesn't match the model's output, error with:
"Embedding dimensions changed (was 1536, now 768). Run with --reset to
rebuild the database."

**Error handling for Ollama:**

Don't auto-install Ollama. Instead, detect and guide:

1. If `provider: "openai"` with a localhost base_url but nothing is listening,
   error: "Could not connect to http://localhost:11434/v1. If using Ollama,
   install it from https://ollama.com and start it with `ollama serve`."

2. If the model isn't pulled (Ollama returns 404), error: "Model
   'nomic-embed-text' not found. Run `ollama pull nomic-embed-text`."

3. If the API key env var is set but empty/missing, error: "Environment
   variable OPENAI_API_KEY is not set."

**Embedding quality notes for documentation:**

Be transparent about quality tradeoffs. Cohere Embed v4 (Bedrock) is
best-in-class for code search — it supports asymmetric embeddings
(`search_document` vs `search_query` input types) and was trained on code.
Local models like nomic-embed-text are ~70-80% as good but work offline
with zero cost. OpenAI text-embedding-3-small is a solid middle ground.

Note: the OpenAI embeddings API does not have document/query type
distinction, so `EmbedDocument` and `EmbedQuery` will produce identical
embeddings. This is fine for most models; only Cohere benefits from the
asymmetric types (handled by the existing Bedrock provider).

### Consider FTS5 hybrid search

The sqlite-vec database already uses SQLite. Adding an FTS5 table alongside
the vector table would enable hybrid search — keyword matching for exact
function/type names combined with semantic vector search for conceptual
queries. The `code_items` metadata table is already there; just need to
create an FTS5 virtual table indexing the same data.
