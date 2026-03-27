---
title: Getting Started Guide
description: A quick guide to setting up and using the project.
---

# Getting Started

Welcome to the project. This guide walks you through setup and basic usage.

## Prerequisites

Before you begin, make sure you have the following installed:

- Go 1.21 or later
- Node.js 20 or later
- AWS CLI (configured with appropriate credentials)

## Installation

Install the CLI tool using Go:

```bash
go install github.com/example/my-tool@latest
```

## Configuration

Create a configuration file in your repository root:

```json
{
  "project": "my-project",
  "sources": [
    {"path": "src", "language": "go"}
  ]
}
```

### Environment Variables

The following environment variables are supported:

| Variable | Description | Default |
|----------|-------------|---------|
| `AWS_REGION` | AWS region for API calls | `us-east-1` |
| `AWS_PROFILE` | AWS credential profile | `default` |

## Usage

### Basic Search

Run a search query from the command line:

```bash
my-tool search "how does authentication work"
```

### Building the Index

To build the full index:

```bash
my-tool all
```

This runs the complete pipeline: parse, generate, build, embed.

## Troubleshooting

### Common Issues

If you encounter authentication errors, make sure your AWS credentials
are configured correctly:

```bash
aws sso login --profile my-profile
```

### Performance

For large codebases, the initial index build may take several minutes.
Subsequent runs are incremental and much faster.
