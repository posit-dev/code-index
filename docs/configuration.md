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
| `c` | `.c`, `.h`, `.cpp`, `.cc`, `.hpp` | tree-sitter — functions, structs, classes, enums, typedefs, Doxygen |
| `cpp` | `.c`, `.h`, `.cpp`, `.cc`, `.hpp` | tree-sitter (same as c) |
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
| `provider` | string | no | `"bedrock"` (default) or `"cli"` (Claude Code CLI) |
| `function_model` | string | yes | Model ID for function-level summaries (high volume, fast) |
| `summary_model` | string | yes | Model ID for file and package summaries (higher quality) |

#### Bedrock model IDs

For AWS Bedrock, use the full model ID including the region prefix:

```json
{
  "provider": "bedrock",
  "function_model": "us.anthropic.claude-haiku-4-5-20251001-v1:0",
  "summary_model": "us.anthropic.claude-sonnet-4-6"
}
```

#### Claude Code CLI

If you prefer to use your existing Claude Code authentication:

```json
{
  "provider": "cli"
}
```

The `function_model` and `summary_model` values are passed as the `--model` flag to `claude -p`. Use model aliases like `"haiku"` or `"sonnet"`.

### `embeddings`

**Type:** object (required for `embed` command)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `provider` | string | no | `"bedrock"` (default) |
| `model` | string | yes | Embedding model ID |

Currently only Bedrock is supported. The embedding model must support the Cohere embedding API format (`texts`, `input_type`, `embedding_types`).

```json
{
  "provider": "bedrock",
  "model": "cohere.embed-v4:0"
}
```

### `storage`

**Type:** object (optional)

Configuration for distributing the vector database via S3.

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

If Rscript is not found, R files are parsed using a regex-based fallback that handles common patterns (function definitions, roxygen comments, S4/R6 classes).

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
