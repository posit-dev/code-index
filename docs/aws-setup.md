# AWS Setup

code-index uses AWS Bedrock for two things:

1. **LLM summaries** — generating natural language descriptions of your code
2. **Vector embeddings** — converting summaries into vectors for similarity search

## Required Bedrock models

You need access to these models in your AWS account:

| Purpose | Model | Model ID |
|---------|-------|----------|
| Function summaries | Claude Haiku 4.5 | `us.anthropic.claude-haiku-4-5-20251001-v1:0` |
| File/package summaries | Claude Sonnet 4.6 | `us.anthropic.claude-sonnet-4-6` |
| Embeddings | Cohere Embed v4 | `cohere.embed-v4:0` |

The Anthropic models are available by default on Bedrock. Cohere Embed v4 is an AWS Marketplace model — a user with marketplace permissions needs to invoke it once to enable it account-wide.

## Credential configuration

code-index uses the standard AWS SDK credential chain. Any of these methods work:

### AWS SSO (recommended for developers)

```bash
# Configure your profile (one-time)
aws configure sso --profile my-profile

# Login before using code-index
aws sso login --profile my-profile

# Set the profile
export AWS_PROFILE=my-profile
```

### Environment variables

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export AWS_SESSION_TOKEN=...    # if using temporary credentials
export AWS_REGION=us-east-1
```

### IAM role (for CI)

In GitHub Actions, use OIDC-based role assumption:

```yaml
- uses: aws-actions/configure-aws-credentials@v5
  with:
    role-to-assume: arn:aws:iam::123456789012:role/code-index-gha
    aws-region: us-east-1
```

See [CI Setup](ci-setup.md) for the full workflow.

## IAM permissions

The IAM role or user needs these permissions:

### For LLM summaries and embeddings

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "bedrock:InvokeModel",
      "Resource": [
        "arn:aws:bedrock:*::foundation-model/anthropic.*",
        "arn:aws:bedrock:*::foundation-model/cohere.*"
      ]
    }
  ]
}
```

### For S3 vector distribution (optional)

If you're distributing the vector database via S3:

```json
{
  "Effect": "Allow",
  "Action": [
    "s3:GetObject",
    "s3:PutObject",
    "s3:ListBucket"
  ],
  "Resource": [
    "arn:aws:s3:::my-code-index",
    "arn:aws:s3:::my-code-index/*"
  ]
}
```

## Profile auto-detection

The `scripts/pull-code-index-vectors.sh` script can automatically find a working AWS profile. Configure it in `.code-index.json`:

```json
{
  "aws": {
    "account": "123456789012",
    "profiles": ["dev", "staging", "production"]
  }
}
```

The script checks the current profile first. If it doesn't match the configured account, it tries each profile in the list until one works.

## Cost estimate

At typical usage (10K items indexed):

| Operation | Model | Cost |
|-----------|-------|------|
| Function summaries | Haiku | ~$0.50 |
| File summaries | Sonnet | ~$1.00 |
| Package summaries | Sonnet | ~$0.50 |
| Embeddings | Cohere Embed v4 | ~$0.16 |
| **Total full rebuild** | | **~$2** |

Incremental runs (typical daily update) cost a few cents.
