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
      id-token: write  # Required for OIDC AWS auth

    steps:
      - uses: actions/checkout@v6

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version-file: 'go.mod'
          cache: true

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
        run: ./scripts/pull-code-index-vectors.sh --quiet || true

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

## AWS setup for CI

### 1. Create an S3 bucket

```bash
aws s3 mb s3://my-code-index --region us-east-1
```

### 2. Create an IAM role for GitHub Actions

Create a role with OIDC trust for your GitHub repository and attach a policy
with `bedrock:InvokeModel` and `s3:GetObject`/`s3:PutObject` permissions.
See [AWS Setup](aws-setup.md) for the policy details.

### 3. Add the secret

In your GitHub repository settings, add `AWS_ROLE_ARN` as a repository secret
with the ARN of the IAM role.

## S3 bucket structure

The workflow uploads a compressed tarball containing all index data:

```
s3://my-code-index/vectors/
├── latest.tar.gz     # All index data (db, docs, cache)
└── latest.sha256     # Hash for freshness checking
```

## Developer setup

Once CI is running, developers just need:

1. AWS credentials that can read from the S3 bucket
2. The MCP server configured in `.mcp.json`

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
