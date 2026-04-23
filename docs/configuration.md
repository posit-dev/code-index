# Configuration

code-index reads its configuration from `.code-index.json` in your repository root.

## Full example

```json
{
  "project": "my-project",
  "sources": [
    {
      "path": "src",
      "language": "go",
      "exclude": ["**/vendor/**", "**/*_test.go", "**/testdata/**"],
      "vendor_include": [
        "github.com/myorg/shared-lib"
      ]
    },
    {
      "path": "frontend/src",
      "language": "typescript",
      "exclude": ["**/*.test.ts", "**/*.spec.ts", "**/__tests__/**"]
    },
    {
      "path": "scripts",
      "language": "python",
      "exclude": ["**/__pycache__/**"]
    },
    {
      "path": "lib",
      "language": "r"
    },
    {
      "path": "docs",
      "language": "markdown",
      "exclude": ["**/_site/**"]
    }
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
    "s3_bucket": "my-code-index",
    "s3_prefix": "vectors"
  },
  "aws": {
    "region": "us-east-1",
    "account": "123456789012",
    "profiles": ["dev", "staging"]
  },
  "r": {
    "executable": "/usr/local/bin/Rscript"
  }
}
```

## Reference

### `project`

**Type:** string (optional)

A name for your project, used in logging output.

### `sources`

**Type:** array of source objects (required)

Each source object defines a directory to index:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | yes | Directory path relative to the repo root |
| `language` | string | yes | One of: `go`, `typescript`, `javascript`, `python`, `r`, `c`, `cpp`, `markdown` |
| `exclude` | string[] | no | Glob patterns for files/directories to skip |
| `import_prefix` | string | no | Go module import prefix (auto-detected from go.mod if empty) |
| `vendor_include` | string[] | no | Go vendor module paths to include (Go only) |

#### Supported languages

| Language value | File extensions | Parser |
|---------------|-----------------|--------|
| `go` | `.go` | Native `go/ast` — functions, types, interfaces, doc comments |
| `typescript` | `.ts`, `.tsx`, `.js`, `.jsx`, `.vue` | tree-sitter — functions, classes, interfaces, enums, JSDoc |
| `javascript` | `.js`, `.jsx` | tree-sitter (same as typescript) |
| `python` | `.py` | tree-sitter — functions, classes, decorators, docstrings |
| `c` | `.c`, `.h` | tree-sitter — functions, structs, enums, typedefs, Doxygen |
| `cpp` | `.cpp`, `.cc`, `.hpp`, `.cxx`, `.hxx` | tree-sitter — functions, classes, structs, namespaces, templates, enums, typedefs, Doxygen |

> **Note:** `.h` files are handled by the `c` parser, not `cpp`. This avoids double-parsing when a project configures both `c` and `cpp` sources over the same tree. C++ projects with `.h` headers should configure a `c` source pointing at the header directories.
| `r` | `.R`, `.r` | Native Rscript with regex fallback — functions, roxygen, S4/R6 classes |
| `markdown` | `.md`, `.qmd` | Regex — headings as sections, YAML front matter |

#### Vendor-aware Go indexing

For Go projects, you can include specific vendored dependencies in the index:

```json
{
  "path": "src",
  "language": "go",
  "vendor_include": [
    "github.com/myorg/shared-lib",
    "github.com/myorg/utils"
  ]
}
```

This indexes the vendored source files and attributes them to their upstream import paths.

### `llm`

**Type:** object (required for `generate` command)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `provider` | string | no | `"bedrock"` (default) or `"openai"` |
| `base_url` | string | no | API base URL (`openai` provider only, default: `https://api.openai.com/v1`) |
| `api_key_env` | string | no | Env var name containing API key (`openai` provider only, default: `OPENAI_API_KEY`) |
| `function_model` | string | yes | Model ID for function-level summaries (high volume, fast) |
| `summary_model` | string | yes | Model ID for file and package summaries (higher quality) |

#### Bedrock (default)

For AWS Bedrock, use the full model ID including the region prefix:

```json
{
  "provider": "bedrock",
  "function_model": "us.anthropic.claude-haiku-4-5-20251001-v1:0",
  "summary_model": "us.anthropic.claude-sonnet-4-6"
}
```

#### OpenAI

```json
{
  "provider": "openai",
  "api_key_env": "OPENAI_API_KEY",
  "function_model": "gpt-4o-mini",
  "summary_model": "gpt-4o"
}
```

#### Ollama (local, no API key)

```json
{
  "provider": "openai",
  "base_url": "http://localhost:11434/v1",
  "function_model": "llama3.2",
  "summary_model": "llama3.2"
}
```

The `openai` provider works with any OpenAI-compatible API: OpenAI, Ollama, Together AI, Groq, Fireworks, LM Studio, vLLM, Azure OpenAI, etc. Set `base_url` to point at the server and `api_key_env` to the env var containing the API key. For local servers like Ollama, the API key is optional.

### `embeddings`

**Type:** object (required for `embed` command)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `provider` | string | no | `"bedrock"` (default) or `"openai"` |
| `base_url` | string | no | API base URL (`openai` provider only, default: `https://api.openai.com/v1`) |
| `api_key_env` | string | no | Env var name containing API key (`openai` provider only, default: `OPENAI_API_KEY`) |
| `model` | string | yes | Embedding model ID |

#### Bedrock (default)

Uses the Cohere embedding API format via Bedrock. Supports asymmetric embeddings (separate document/query types) for best retrieval quality.

```json
{
  "provider": "bedrock",
  "model": "cohere.embed-v4:0"
}
```

#### OpenAI

```json
{
  "provider": "openai",
  "api_key_env": "OPENAI_API_KEY",
  "model": "text-embedding-3-small"
}
```

#### Ollama (local, no API key)

```json
{
  "provider": "openai",
  "base_url": "http://localhost:11434/v1",
  "model": "nomic-embed-text"
}
```

The embedding model must be consistent between indexing and querying — you can't index with one model and search with another. Embedding dimensions are detected automatically from the model's output. If you switch models, run `code-index embed --reset` to rebuild the database.

**Quality note:** Cohere Embed v4 (Bedrock) gives the best code search results thanks to asymmetric document/query embeddings and code-specific training. OpenAI `text-embedding-3-small` is a solid middle ground. Ollama models like `nomic-embed-text` work well for local development at no cost but are ~70-80% the quality of Cohere for code search.

### `storage`

**Type:** object (optional)

Configuration for distributing the vector database to your team. Two
providers are supported, auto-detected from which fields are set:

**HTTP URL** (works with any hosting):

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `url` | string | no | HTTPS URL to the vector database tarball |
| `auth_token_env` | string | no | Env var name containing a bearer token for authenticated downloads |

The SHA URL is derived automatically as `{url}.sha256`.

```json
{
  "storage": {
    "url": "https://github.com/myorg/myrepo/releases/download/code-index/code-index.tar.gz",
    "auth_token_env": "GITHUB_TOKEN"
  }
}
```

**S3** (AWS enterprise):

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `s3_bucket` | string | no | S3 bucket name |
| `s3_prefix` | string | no | Key prefix within the bucket (default: `"vectors"`) |

### `aws`

**Type:** object (optional)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `region` | string | no | AWS region (default: `"us-east-1"`) |
| `account` | string | no | AWS account ID for profile auto-detection |
| `profiles` | string[] | no | AWS profile names to try when auto-detecting credentials |

The `account` and `profiles` fields are used by `scripts/pull-code-index-vectors.sh` to automatically find a working AWS profile. When the current profile doesn't match the configured account, it tries each profile in order.

### `r`

**Type:** object (optional)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `executable` | string | no | Path to `Rscript` (auto-detected from PATH if empty) |

R parsing works in two modes:

1. **Native mode** (recommended) — uses Rscript to parse R files with full
   accuracy, including complex expressions, S4/R6 class detection, and
   roxygen2 documentation. Requires R to be installed.

2. **Regex fallback** — when Rscript is not available, uses regex patterns
   to extract function definitions, roxygen comments, and common class
   patterns. Works for most R code but may miss unusual constructs.

To install R:
- **macOS**: `brew install r` or download from [CRAN](https://cloud.r-project.org/)
- **Ubuntu/Debian**: `sudo apt-get install r-base`
- **Fedora/RHEL**: `sudo dnf install R`
- **Windows**: download from [CRAN](https://cloud.r-project.org/)

If R is installed in a non-standard location, set the `executable` field:
```json
{"r": {"executable": "/opt/R/4.4.0/bin/Rscript"}}
```

## File layout

The tool generates files in `.code-index/` (add this to `.gitignore`):

```
.code-index/
├── code-index.db      # SQLite database with vectors and metadata
├── parsed.json        # AST extraction output (transient)
├── index.json         # Searchable JSON index
├── cache.json         # LLM doc generation cache
├── embed_cache.json   # Embedding cache for incremental updates
├── docs/              # Generated LLM summaries
│   ├── func/          # Function-level summaries
│   ├── file/          # File-level summaries
│   └── pkg/           # Package-level summaries
└── .vectors-sha256    # S3 download freshness check
```

All of these are generated and should be in `.gitignore`.
