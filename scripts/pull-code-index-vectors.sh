#!/usr/bin/env bash
# Copyright (C) 2026 by Posit Software, PBC
# Licensed under the MIT License. See LICENSE for details.
# Pull the latest code-index vector database from S3.
# Reads configuration from .code-index.json in the repository root.
#
# Usage:
#   ./scripts/pull-code-index-vectors.sh          # pull if outdated
#   ./scripts/pull-code-index-vectors.sh --force   # always pull

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG_FILE="${REPO_ROOT}/.code-index.json"
INDEX_DIR="${REPO_ROOT}/.code-index"
DB_FILE="${INDEX_DIR}/code-index.db"
LOCAL_SHA_FILE="${INDEX_DIR}/.vectors-sha256"

# Read config from .code-index.json using python3 (universally available).
read_config() {
    local key="$1"
    local default="${2:-}"
    local value
    if [ -f "$CONFIG_FILE" ]; then
        value=$(python3 -c "
import json, sys
with open('$CONFIG_FILE') as f:
    cfg = json.load(f)
keys = '$key'.split('.')
v = cfg
for k in keys:
    v = v.get(k, None) if isinstance(v, dict) else None
    if v is None:
        break
if isinstance(v, list):
    print(' '.join(v))
elif v is not None:
    print(v)
" 2>/dev/null) || value=""
    fi
    echo "${value:-$default}"
}

BUCKET=$(read_config "storage.s3_bucket")
S3_PREFIX=$(read_config "storage.s3_prefix" "vectors")
TARGET_ACCOUNT=$(read_config "aws.account")
AWS_PROFILES=$(read_config "aws.profiles")
export AWS_REGION=$(read_config "aws.region" "us-east-1")

if [ -z "$BUCKET" ]; then
    echo "Error: storage.s3_bucket not set in $CONFIG_FILE" >&2
    exit 0
fi

FORCE=false
QUIET=false
for arg in "$@"; do
    case "$arg" in
        --force) FORCE=true ;;
        --quiet) QUIET=true ;;
    esac
done

log() {
    if [ "$QUIET" = false ]; then
        echo "$@" >&2
    fi
}

# Find a working AWS profile that can access the target account.
find_working_profile() {
    # If CODE_INDEX_AWS_PROFILE is explicitly set, use it
    if [ -n "${CODE_INDEX_AWS_PROFILE:-}" ]; then
        export AWS_PROFILE="${CODE_INDEX_AWS_PROFILE}"
        if check_account; then
            return 0
        fi
        log "Warning: CODE_INDEX_AWS_PROFILE=${CODE_INDEX_AWS_PROFILE} does not have access to account ${TARGET_ACCOUNT}."
        return 1
    fi

    # If no target account configured, just check current credentials work
    if [ -z "$TARGET_ACCOUNT" ]; then
        if aws sts get-caller-identity >/dev/null 2>&1; then
            log "Using current AWS credentials."
            return 0
        fi
        return 1
    fi

    # Try current profile first
    if check_account; then
        log "Using current AWS profile (${AWS_PROFILE:-default})."
        return 0
    fi

    # Try profiles from config
    for profile in $AWS_PROFILES; do
        export AWS_PROFILE="$profile"
        if check_account; then
            log "Using AWS profile: ${profile}"
            return 0
        fi
    done

    return 1
}

# Check if the current AWS credentials are for the target account
check_account() {
    local account
    account=$(aws sts get-caller-identity --query "Account" --output text 2>/dev/null) || return 1
    [ "$account" = "$TARGET_ACCOUNT" ]
}

if ! find_working_profile; then
    log "Warning: No AWS profile found with access to the configured account."
    log "Check aws.profiles in $CONFIG_FILE and run 'aws sso login --profile <profile>'."
    log "code_search will fall back to grep/read tools."
    exit 0  # Don't fail — this is a best-effort operation
fi

# Get the remote SHA
REMOTE_SHA=""
if ! REMOTE_SHA=$(aws s3 cp "s3://${BUCKET}/${S3_PREFIX}/latest.sha256" - 2>/dev/null); then
    REMOTE_SHA=""
fi
if [ -z "$REMOTE_SHA" ]; then
    log "Warning: Could not fetch remote vector hash from S3. Skipping update."
    exit 0
fi

# Check if local database is current
if [ "$FORCE" = false ] && [ -f "$LOCAL_SHA_FILE" ] && [ -f "$DB_FILE" ]; then
    LOCAL_SHA=$(cat "$LOCAL_SHA_FILE" 2>/dev/null || echo "")
    if [ "$LOCAL_SHA" = "$REMOTE_SHA" ]; then
        log "Vector database is up to date."
        exit 0
    fi
fi

log "Downloading vector database from S3..."
TMPFILE=$(mktemp)
trap "rm -f $TMPFILE" EXIT

aws s3 cp "s3://${BUCKET}/${S3_PREFIX}/latest.tar.gz" "$TMPFILE" --quiet

# Extract to .code-index/ (includes code-index.db, embed_cache.json, docs/, cache.json)
mkdir -p "$INDEX_DIR"
tar xzf "$TMPFILE" -C "$INDEX_DIR"

# Save the SHA for future freshness checks
echo "$REMOTE_SHA" > "$LOCAL_SHA_FILE"

log "Code index database updated ($(du -sh "$DB_FILE" 2>/dev/null | awk '{print $1}'))."
