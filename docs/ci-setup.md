# CI Setup

Automate nightly index updates so your team always has current search results.

## Overview

The recommended setup:

1. A **nightly GitHub Actions workflow** runs the full pipeline (parse → generate → build → embed)
2. Updated data is **uploaded to S3** for team distribution
3. Developers' MCP tools **pull from S3** automatically when the database is outdated

## GitHub Actions workflow

Create `.github/workflows/code-index.yml` in your repository:

```yaml
name: Code Index

on:
  schedule:
    # Run nightly at 3am UTC
    - cron: '0 3 * * *'
  workflow_dispatch:
    inputs:
      reset:
        description: 'Rebuild all embeddings from scratch'
        required: false
        default: 'false'
        type: boolean

jobs:
  update-code-index:
    runs-on: ubuntu-latest
    timeout-minutes: 60
    permissions:
      contents: read
      id-token: write  # Required for OIDC AWS auth

    steps:
      - uses: actions/checkout@v6

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version: '1.26'
          cache: true

      - name: Install libsqlite3-dev
        run: sudo apt-get install -y libsqlite3-dev

      - name: Install code-index
        run: go install github.com/posit-dev/code-index/cmd/code-index@latest

      - name: Read config
        id: config
        run: |
          S3_BUCKET=$(python3 -c "import json; print(json.load(open('.code-index.json')).get('storage',{}).get('s3_bucket',''))")
          S3_PREFIX=$(python3 -c "import json; print(json.load(open('.code-index.json')).get('storage',{}).get('s3_prefix','vectors'))")
          AWS_REGION=$(python3 -c "import json; print(json.load(open('.code-index.json')).get('aws',{}).get('region','us-east-1'))")
          echo "s3_bucket=$S3_BUCKET" >> "$GITHUB_OUTPUT"
          echo "s3_prefix=$S3_PREFIX" >> "$GITHUB_OUTPUT"
          echo "aws_region=$AWS_REGION" >> "$GITHUB_OUTPUT"

      - name: Configure AWS credentials
        uses: aws-actions/configure-aws-credentials@v5
        with:
          role-to-assume: ${{ secrets.AWS_ROLE_ARN }}
          role-session-name: code-index-${{ github.run_id }}
          aws-region: ${{ steps.config.outputs.aws_region }}

      - name: Pull existing index data
        if: github.event.inputs.reset != 'true'
        run: |
          S3_BUCKET="${{ steps.config.outputs.s3_bucket }}"
          S3_PREFIX="${{ steps.config.outputs.s3_prefix }}"
          mkdir -p .code-index
          aws s3 cp "s3://${S3_BUCKET}/${S3_PREFIX}/latest.tar.gz" /tmp/code-index.tar.gz --quiet || true
          if [ -f /tmp/code-index.tar.gz ]; then
            tar xzf /tmp/code-index.tar.gz -C .code-index
            echo "Pulled existing index data from S3"
          fi

      - name: Parse source files
        run: code-index parse

      - name: Generate LLM summaries
        run: code-index generate

      - name: Build search index
        run: code-index build

      - name: Generate embeddings
        run: |
          RESET_FLAG=""
          if [ "${{ github.event.inputs.reset }}" = "true" ]; then
            RESET_FLAG="--reset"
          fi
          code-index embed $RESET_FLAG

      - name: Upload to S3
        run: |
          S3_BUCKET="${{ steps.config.outputs.s3_bucket }}"
          S3_PREFIX="${{ steps.config.outputs.s3_prefix }}"
          tar czf /tmp/code-index.tar.gz -C .code-index \
            code-index.db docs/ embed_cache.json cache.json index.json
          shasum -a 256 /tmp/code-index.tar.gz | awk '{print $1}' > /tmp/code-index.sha256
          aws s3 cp /tmp/code-index.tar.gz "s3://${S3_BUCKET}/${S3_PREFIX}/latest.tar.gz" --quiet
          aws s3 cp /tmp/code-index.sha256 "s3://${S3_BUCKET}/${S3_PREFIX}/latest.sha256" --quiet
```

## Storage options

The CI workflow uploads the index tarball to a hosting provider. Developers
download it automatically via the pull script or MCP auto-update.

### Option A: GitHub Releases (simplest, no cloud needed)

Add an upload step to your workflow:

```yaml
      - name: Upload to GitHub Release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          tar czf /tmp/code-index.tar.gz -C .code-index \
            code-index.db docs/ embed_cache.json cache.json index.json
          shasum -a 256 /tmp/code-index.tar.gz | awk '{print $1}' > /tmp/code-index.tar.gz.sha256
          gh release create code-index --title "Code Index" --notes "Auto-updated" 2>/dev/null || true
          gh release upload code-index /tmp/code-index.tar.gz /tmp/code-index.tar.gz.sha256 --clobber
```

Configure in `.code-index.json`:
```json
{
  "storage": {
    "url": "https://github.com/OWNER/REPO/releases/download/code-index/code-index.tar.gz"
  }
}
```

For private repos, set `"auth_token_env": "GITHUB_TOKEN"` and ensure
developers have `GITHUB_TOKEN` set (the `gh` CLI sets this automatically).

### Option B: S3 (AWS enterprise)

```yaml
      - name: Upload to S3
        run: |
          tar czf /tmp/code-index.tar.gz -C .code-index \
            code-index.db docs/ embed_cache.json cache.json index.json
          shasum -a 256 /tmp/code-index.tar.gz | awk '{print $1}' > /tmp/code-index.tar.gz.sha256
          aws s3 cp /tmp/code-index.tar.gz "s3://${S3_BUCKET}/${S3_PREFIX}/latest.tar.gz" --quiet
          aws s3 cp /tmp/code-index.tar.gz.sha256 "s3://${S3_BUCKET}/${S3_PREFIX}/latest.sha256" --quiet
```

Requires an S3 bucket and IAM role. See [AWS Setup](aws-setup.md).

### Option C: Any HTTPS endpoint

Upload however you like — GCS (`gcloud storage cp`), Azure Blob
(`az storage blob upload`), Artifactory, a static file server, etc.
As long as the tarball and `.sha256` file are accessible over HTTPS,
the pull script handles the rest.

```json
{
  "storage": {
    "url": "https://your-host.example.com/path/to/code-index.tar.gz",
    "auth_token_env": "MY_TOKEN"
  }
}
```

## Developer setup

Once CI is running, developers just need:

1. The MCP server configured in `.mcp.json`
2. For private storage: the auth token env var set (e.g., `GITHUB_TOKEN`)
3. For S3: AWS credentials that can read from the bucket

The `pull-code-index-vectors.sh` script handles downloading and caching.
If the MCP tool can't find a local database, it runs the pull script
automatically.

## Manual trigger

To rebuild immediately (e.g., after a major refactor):

```bash
gh workflow run code-index.yml

# Or with full rebuild:
gh workflow run code-index.yml -f reset=true
```

## Cost

The nightly CI run costs:

| Component | Cost per run |
|-----------|-------------|
| GitHub Actions | ~2 minutes of ubuntu-latest |
| Bedrock LLM (incremental) | A few cents |
| Bedrock embeddings (incremental) | A few cents |
| S3 storage | ~$0.01/month |

A full rebuild is ~$2 in Bedrock costs.
