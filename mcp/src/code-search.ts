// Copyright (C) 2026 by Posit Software, PBC
// Licensed under the MIT License. See LICENSE for details.

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";
import { execSync } from "child_process";
import { resolve } from "path";
import {
  existsSync,
  readFileSync,
  writeFileSync,
  mkdirSync,
  renameSync,
  statSync,
} from "fs";
import { tmpdir } from "os";
import Database from "better-sqlite3";
import * as sqliteVec from "sqlite-vec";
import {
  BedrockRuntimeClient,
  InvokeModelCommand,
} from "@aws-sdk/client-bedrock-runtime";
import { fromNodeProviderChain } from "@aws-sdk/credential-providers";

const CodeSearchArgsSchema = z.object({
  query: z
    .string()
    .describe(
      "Search query — can be a function name, concept description, or natural language question like 'string helpers' or 'how does caching work'"
    ),
  maxResults: z
    .number()
    .default(15)
    .describe("Maximum number of results to return (default: 15)"),
});

interface CodeIndexConfig {
  embeddings?: {
    provider?: string;
    model?: string;
    base_url?: string;
    api_key_env?: string;
  };
  storage?: {
    url?: string;
    auth_token_env?: string;
    s3_bucket?: string;
    s3_prefix?: string;
  };
  aws?: {
    region?: string;
    account?: string;
    profiles?: string[];
  };
  search?: {
    alpha?: number;
  };
}

// --- Database discovery ---

function findDatabase(cwd: string): string | null {
  let dir = cwd;
  while (dir !== "/") {
    const candidate = resolve(dir, ".code-index", "code-index.db");
    if (existsSync(candidate)) return candidate;
    dir = resolve(dir, "..");
  }
  return null;
}

function findRepoRoot(cwd: string): string | null {
  let dir = cwd;
  while (dir !== "/") {
    if (existsSync(resolve(dir, ".code-index.json"))) return dir;
    dir = resolve(dir, "..");
  }
  return null;
}

function loadConfig(repoRoot: string): CodeIndexConfig {
  const configPath = resolve(repoRoot, ".code-index.json");
  if (!existsSync(configPath)) return {};
  try {
    return JSON.parse(readFileSync(configPath, "utf-8")) as CodeIndexConfig;
  } catch {
    return {};
  }
}

// --- Auto-update ---

// Track in-flight background downloads to avoid duplicates.
let updateInProgress = false;

/**
 * Check if the vector database is stale and trigger a background update.
 * Never blocks — returns immediately. The updated DB is used on the next search.
 */
function maybeUpdateInBackground(
  repoRoot: string,
  config: CodeIndexConfig
): void {
  if (updateInProgress) return;

  const indexDir = resolve(repoRoot, ".code-index");
  const shaFile = resolve(indexDir, ".vectors-sha256");

  // Only check once per hour.
  if (existsSync(shaFile)) {
    try {
      const stat = statSync(shaFile);
      const ageMs = Date.now() - stat.mtimeMs;
      if (ageMs < 3600_000) return; // Less than 1 hour old
    } catch {
      // Can't stat — proceed with check
    }
  }

  const storageUrl = config.storage?.url;
  const s3Bucket = config.storage?.s3_bucket;

  if (!storageUrl && !s3Bucket) return; // No storage configured

  updateInProgress = true;

  (async () => {
    try {
      // Get remote SHA.
      let remoteSha: string | null = null;
      if (storageUrl) {
        remoteSha = await fetchUrlSha(storageUrl, config);
      } else if (s3Bucket) {
        remoteSha = await fetchS3Sha(config);
      }

      if (!remoteSha) {
        // Touch the SHA file to avoid re-checking for another hour.
        touchFile(shaFile, indexDir);
        return;
      }

      // Compare with local SHA.
      const localSha = existsSync(shaFile)
        ? readFileSync(shaFile, "utf-8").trim()
        : "";

      if (localSha === remoteSha) {
        // Up to date — touch file to reset the check timer.
        touchFile(shaFile, indexDir);
        return;
      }

      // Download to temp file, then atomic swap.
      const tmpFile = resolve(tmpdir(), `code-index-${Date.now()}.tar.gz`);
      try {
        if (storageUrl) {
          await downloadUrl(storageUrl, tmpFile, config);
        } else {
          await downloadS3(tmpFile, config);
        }

        // Extract.
        mkdirSync(indexDir, { recursive: true });
        execSync(`tar xzf "${tmpFile}" -C "${indexDir}"`, { timeout: 30000 });

        // Write the new SHA.
        writeFileSync(shaFile, remoteSha + "\n");
      } finally {
        try {
          execSync(`rm -f "${tmpFile}"`);
        } catch {
          // Cleanup best-effort
        }
      }
    } catch {
      // Background update failed silently — don't disrupt searches.
    } finally {
      updateInProgress = false;
    }
  })();
}

function touchFile(path: string, dir: string): void {
  try {
    mkdirSync(dir, { recursive: true });
    writeFileSync(path, existsSync(path) ? readFileSync(path) : "");
  } catch {
    // best-effort
  }
}

async function fetchUrlSha(
  storageUrl: string,
  config: CodeIndexConfig
): Promise<string | null> {
  const shaUrl = storageUrl + ".sha256";
  const headers: Record<string, string> = {};
  const tokenEnv = config.storage?.auth_token_env;
  if (tokenEnv && process.env[tokenEnv]) {
    headers["Authorization"] = `Bearer ${process.env[tokenEnv]}`;
  }
  try {
    const resp = await fetch(shaUrl, { headers });
    if (!resp.ok) return null;
    const text = await resp.text();
    return text.trim().split(/\s/)[0] || null;
  } catch {
    return null;
  }
}

async function downloadUrl(
  storageUrl: string,
  destPath: string,
  config: CodeIndexConfig
): Promise<void> {
  const headers: Record<string, string> = {};
  const tokenEnv = config.storage?.auth_token_env;
  if (tokenEnv && process.env[tokenEnv]) {
    headers["Authorization"] = `Bearer ${process.env[tokenEnv]}`;
  }
  const resp = await fetch(storageUrl, { headers });
  if (!resp.ok) throw new Error(`Download failed: ${resp.status}`);
  const buffer = Buffer.from(await resp.arrayBuffer());
  writeFileSync(destPath, buffer);
}

/**
 * Find a working AWS profile that can access the configured account.
 * Tries profiles from .code-index.json aws.profiles until one matches
 * the configured aws.account. Returns the env vars to use for AWS CLI calls.
 */
function findAwsEnv(config: CodeIndexConfig): Record<string, string> {
  const env = { ...process.env } as Record<string, string>;
  const targetAccount = config.aws?.account;
  const region = config.aws?.region || "us-east-1";
  env["AWS_REGION"] = region;

  // If CODE_INDEX_AWS_PROFILE is explicitly set, use it.
  if (process.env["CODE_INDEX_AWS_PROFILE"]) {
    env["AWS_PROFILE"] = process.env["CODE_INDEX_AWS_PROFILE"];
    return env;
  }

  // If no target account configured, use current credentials as-is.
  if (!targetAccount) return env;

  // Try current profile first.
  try {
    const account = execSync(
      'aws sts get-caller-identity --query "Account" --output text 2>/dev/null',
      { timeout: 10000, encoding: "utf-8", env }
    ).trim();
    if (account === targetAccount) return env;
  } catch {
    // Current profile doesn't work
  }

  // Try each configured profile.
  const profiles = config.aws?.profiles || [];
  for (const profile of profiles) {
    const tryEnv = { ...env, AWS_PROFILE: profile };
    try {
      const account = execSync(
        'aws sts get-caller-identity --query "Account" --output text 2>/dev/null',
        { timeout: 10000, encoding: "utf-8", env: tryEnv }
      ).trim();
      if (account === targetAccount) return tryEnv;
    } catch {
      // This profile doesn't work, try next
    }
  }

  // No matching profile found — return default env and let it fail
  // with a clear error from the AWS CLI.
  return env;
}

async function fetchS3Sha(config: CodeIndexConfig): Promise<string | null> {
  const bucket = config.storage?.s3_bucket;
  const prefix = config.storage?.s3_prefix || "vectors";
  if (!bucket) return null;
  const env = findAwsEnv(config);
  try {
    const result = execSync(
      `aws s3 cp "s3://${bucket}/${prefix}/latest.sha256" - 2>/dev/null`,
      { timeout: 15000, encoding: "utf-8", env }
    );
    return result.trim().split(/\s/)[0] || null;
  } catch {
    return null;
  }
}

async function downloadS3(
  destPath: string,
  config: CodeIndexConfig
): Promise<void> {
  const bucket = config.storage?.s3_bucket;
  const prefix = config.storage?.s3_prefix || "vectors";
  const env = findAwsEnv(config);
  execSync(`aws s3 cp "s3://${bucket}/${prefix}/latest.tar.gz" "${destPath}" --quiet`, {
    timeout: 120000,
    env,
  });
}

/**
 * Initial pull of the vector database from configured storage.
 * Blocks until complete — only called when no local database exists.
 */
async function initialPull(
  repoRoot: string,
  config: CodeIndexConfig
): Promise<void> {
  const storageUrl = config.storage?.url;
  const s3Bucket = config.storage?.s3_bucket;

  if (!storageUrl && !s3Bucket) {
    // Try the legacy pull script as a fallback.
    const pullScript = resolve(repoRoot, "scripts", "pull-code-index-vectors.sh");
    if (existsSync(pullScript)) {
      execSync(pullScript + " --quiet", {
        cwd: repoRoot,
        timeout: 60000,
        env: { ...process.env },
      });
    }
    return;
  }

  const indexDir = resolve(repoRoot, ".code-index");
  const tmpFile = resolve(tmpdir(), `code-index-init-${Date.now()}.tar.gz`);

  try {
    if (storageUrl) {
      await downloadUrl(storageUrl, tmpFile, config);
    } else {
      await downloadS3(tmpFile, config);
    }

    mkdirSync(indexDir, { recursive: true });
    execSync(`tar xzf "${tmpFile}" -C "${indexDir}"`, { timeout: 30000 });

    // Fetch and save SHA for future freshness checks.
    let sha: string | null = null;
    if (storageUrl) {
      sha = await fetchUrlSha(storageUrl, config);
    } else {
      sha = await fetchS3Sha(config);
    }
    if (sha) {
      writeFileSync(resolve(indexDir, ".vectors-sha256"), sha + "\n");
    }
  } finally {
    try {
      execSync(`rm -f "${tmpFile}"`);
    } catch {
      // cleanup best-effort
    }
  }
}

// --- Embedding ---

async function embedQueryOpenAI(
  query: string,
  config: CodeIndexConfig
): Promise<number[]> {
  const baseURL = (
    config.embeddings?.base_url || "https://api.openai.com/v1"
  ).replace(/\/+$/, "");
  const model = config.embeddings?.model || "text-embedding-3-small";
  const apiKeyEnv = config.embeddings?.api_key_env || "OPENAI_API_KEY";
  const apiKey = process.env[apiKeyEnv] || "";

  const url = `${baseURL}/embeddings`;
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };
  if (apiKey) {
    headers["Authorization"] = `Bearer ${apiKey}`;
  }

  const response = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify({ model, input: query }),
  });

  if (!response.ok) {
    const body = await response.text();
    if (response.status === 404 && body.toLowerCase().includes("not found")) {
      throw new Error(
        `Model '${model}' not found. Run \`ollama pull ${model}\` to download it.`
      );
    }
    throw new Error(`Embedding API error ${response.status}: ${body}`);
  }

  const result = (await response.json()) as {
    data: Array<{ embedding: number[] }>;
  };

  if (!result.data?.[0]?.embedding) {
    throw new Error("Empty embedding response");
  }

  return result.data[0].embedding;
}

async function embedQueryBedrock(
  query: string,
  config: CodeIndexConfig
): Promise<number[]> {
  const region = config.aws?.region || "us-east-1";
  const model = config.embeddings?.model || "cohere.embed-v4:0";

  const client = new BedrockRuntimeClient({
    region,
    credentials: fromNodeProviderChain(),
  });

  const body = JSON.stringify({
    texts: [query],
    input_type: "search_query",
    embedding_types: ["float"],
  });

  const command = new InvokeModelCommand({
    modelId: model,
    contentType: "application/json",
    accept: "application/json",
    body: new TextEncoder().encode(body),
  });

  const response = await client.send(command);
  const result = JSON.parse(new TextDecoder().decode(response.body)) as {
    embeddings: { float: number[][] };
  };

  if (!result.embeddings?.float?.[0]) {
    throw new Error("Empty embedding response from Bedrock");
  }

  return result.embeddings.float[0];
}

async function embedQuery(
  query: string,
  config: CodeIndexConfig
): Promise<number[]> {
  const provider = config.embeddings?.provider || "bedrock";
  switch (provider) {
    case "openai":
      return embedQueryOpenAI(query, config);
    case "bedrock":
      return embedQueryBedrock(query, config);
    default:
      throw new Error(
        `Unknown embedding provider "${provider}" (supported: "bedrock", "openai")`
      );
  }
}

// --- Database search ---

const FTS5_SPECIAL_CHARS = /[*"(){}+\-:]/g;
const FTS5_RESERVED = new Set(["and", "or", "not", "near"]);

function sanitizeFTS5Query(query: string): string {
  const terms = query
    .split(/\s+/)
    .map((t) => t.replace(FTS5_SPECIAL_CHARS, ""))
    .filter((t) => t.length > 0 && !FTS5_RESERVED.has(t.toLowerCase()));
  return terms.join(" OR ");
}

function searchDatabase(
  dbPath: string,
  queryEmbedding: number[],
  queryText: string,
  maxResults: number,
  alpha: number
): Array<{
  rank: number;
  similarity: number;
  metadata: Record<string, string>;
}> {
  const db = new Database(dbPath, { readonly: true });
  sqliteVec.load(db);

  try {
    if (alpha >= 1.0) {
      return vectorOnlySearch(db, queryEmbedding, maxResults);
    }

    // Step 1 — Vector candidates (3x over-fetch for fusion)
    const vecLimit = maxResults * 3;
    const vecRows = db.prepare(`
      SELECT v.rowid, v.distance, c.doc_id
      FROM vec_items v
      JOIN code_items c ON c.id = v.rowid
      WHERE v.embedding MATCH ?
        AND k = ?
      ORDER BY v.distance
    `).all(
      JSON.stringify(queryEmbedding),
      vecLimit
    ) as Array<{
      rowid: number;
      distance: number;
      doc_id: string;
    }>;

    const vecScores = new Map<string, number>();
    for (const row of vecRows) {
      const score = Math.max(0, Math.min(1, 1 - row.distance));
      vecScores.set(row.doc_id, score);
    }

    // Step 2 — BM25 candidates
    const bm25Scores = new Map<string, number>();
    const ftsQuery = sanitizeFTS5Query(queryText);
    if (ftsQuery.length > 0) {
      try {
        const ftsRows = db.prepare(`
          SELECT c.doc_id, code_items_fts.rank AS bm25_score
          FROM code_items_fts
          JOIN code_items c ON c.id = code_items_fts.rowid
          WHERE code_items_fts MATCH ?
          ORDER BY code_items_fts.rank
          LIMIT ?
        `).all(ftsQuery, vecLimit) as Array<{
          doc_id: string;
          bm25_score: number;
        }>;

        for (const row of ftsRows) {
          bm25Scores.set(row.doc_id, -row.bm25_score);
        }
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        if (!msg.includes("no such table")) throw e;
      }
    }

    // Step 3 — Normalize and combine
    const allDocIds = new Set([
      ...vecScores.keys(),
      ...bm25Scores.keys(),
    ]);

    const vecValues = [...vecScores.values()];
    const bm25Values = [...bm25Scores.values()];

    const minVec = vecValues.length
      ? vecValues.reduce((a, b) => Math.min(a, b), Infinity) : 0;
    const maxVec = vecValues.length
      ? vecValues.reduce((a, b) => Math.max(a, b), -Infinity) : 0;
    const minBm25 = bm25Values.length
      ? bm25Values.reduce((a, b) => Math.min(a, b), Infinity) : 0;
    const maxBm25 = bm25Values.length
      ? bm25Values.reduce((a, b) => Math.max(a, b), -Infinity) : 0;

    const vecRange = maxVec - minVec;
    const bm25Range = maxBm25 - minBm25;

    const scored: Array<{ docId: string; hybridScore: number }> = [];
    for (const docId of allDocIds) {
      const rawVec = vecScores.get(docId);
      const rawBm25 = bm25Scores.get(docId);

      const vecNorm = rawVec === undefined
        ? 0
        : vecRange === 0 ? 1.0 : (rawVec - minVec) / vecRange;
      const bm25Norm = rawBm25 === undefined
        ? 0
        : bm25Range === 0 ? 1.0 : (rawBm25 - minBm25) / bm25Range;

      const hybridScore =
        alpha * vecNorm + (1 - alpha) * bm25Norm;
      scored.push({ docId, hybridScore });
    }

    scored.sort((a, b) => b.hybridScore - a.hybridScore);
    const topDocs = scored.slice(0, maxResults);

    if (topDocs.length === 0) return [];

    // Step 4 — Fetch metadata for top results
    const placeholders = topDocs.map(() => "?").join(",");
    const metaRows = db.prepare(`
      SELECT doc_id, kind, name, signature,
        file, line, receiver, package, summary, doc
      FROM code_items
      WHERE doc_id IN (${placeholders})
    `).all(
      ...topDocs.map((d) => d.docId)
    ) as Array<{
      doc_id: string;
      kind: string;
      name: string;
      signature: string | null;
      file: string;
      line: number;
      receiver: string | null;
      package: string | null;
      summary: string | null;
      doc: string | null;
    }>;

    const metaByDocId = new Map(
      metaRows.map((r) => [r.doc_id, r])
    );

    return topDocs
      .filter((d) => metaByDocId.has(d.docId))
      .map((d, i) => {
        const row = metaByDocId.get(d.docId)!;
        const metadata: Record<string, string> = {
          kind: row.kind,
          name: row.name,
          file: row.file,
          line: String(row.line),
        };
        if (row.signature) metadata["signature"] = row.signature;
        if (row.receiver) metadata["receiver"] = row.receiver;
        if (row.package) metadata["package"] = row.package;
        if (row.summary) metadata["summary"] = row.summary;
        if (row.doc) metadata["doc"] = row.doc;

        return {
          rank: i + 1,
          similarity: d.hybridScore,
          metadata,
        };
      });
  } finally {
    db.close();
  }
}

function vectorOnlySearch(
  db: ReturnType<typeof Database>,
  queryEmbedding: number[],
  maxResults: number
): Array<{
  rank: number;
  similarity: number;
  metadata: Record<string, string>;
}> {
  const rows = db.prepare(`
    SELECT v.rowid, v.distance,
      c.doc_id, c.kind, c.name, c.signature,
      c.file, c.line, c.receiver, c.package, c.summary, c.doc
    FROM vec_items v
    JOIN code_items c ON c.id = v.rowid
    WHERE v.embedding MATCH ?
      AND k = ?
    ORDER BY v.distance
  `).all(
    JSON.stringify(queryEmbedding),
    maxResults
  ) as Array<{
    rowid: number;
    distance: number;
    doc_id: string;
    kind: string;
    name: string;
    signature: string | null;
    file: string;
    line: number;
    receiver: string | null;
    package: string | null;
    summary: string | null;
    doc: string | null;
  }>;

  return rows.map((row, i) => {
    const similarity = Math.max(0, 1 - row.distance);
    const metadata: Record<string, string> = {
      kind: row.kind,
      name: row.name,
      file: row.file,
      line: String(row.line),
    };
    if (row.signature) metadata["signature"] = row.signature;
    if (row.receiver) metadata["receiver"] = row.receiver;
    if (row.package) metadata["package"] = row.package;
    if (row.summary) metadata["summary"] = row.summary;
    if (row.doc) metadata["doc"] = row.doc;

    return { rank: i + 1, similarity, metadata };
  });
}

// --- MCP tool registration ---

export function registerCodeSearchTool(server: McpServer): void {
  server.tool(
    "code_search",
    "Search the codebase using semantic vector search. " +
      "Use this to find existing utilities before writing new ones, understand how " +
      "patterns are implemented across the codebase, or navigate the architecture. " +
      "Requires the vector database to be built and an embedding provider configured.",
    CodeSearchArgsSchema.shape,
    async (args) => {
      const parsed = CodeSearchArgsSchema.parse(args);

      try {
        const cwd = process.cwd();
        const repoRoot = findRepoRoot(cwd);
        const config = repoRoot ? loadConfig(repoRoot) : {};

        // Find the database.
        let dbPath = findDatabase(cwd);
        let pullError: string | undefined;
        if (!dbPath && repoRoot) {
          // No local database — try to download from configured storage.
          // This blocks on first use but is needed to bootstrap.
          try {
            await initialPull(repoRoot, config);
            dbPath = findDatabase(cwd);
          } catch (e) {
            pullError = e instanceof Error ? e.message : String(e);
          }
        }

        if (!dbPath) {
          let errorMsg =
            "Vector database not found. Run 'code-index all' to build the index, " +
            "or ensure .code-index/code-index.db exists.";
          if (pullError) {
            errorMsg += ` Auto-download failed: ${pullError}`;
          }
          if (!repoRoot) {
            errorMsg += " Could not find .code-index.json in any parent directory.";
          } else if (!config.storage?.s3_bucket && !config.storage?.url) {
            errorMsg += " No storage configured in .code-index.json (set storage.s3_bucket or storage.url).";
          }
          return {
            content: [
              {
                type: "text" as const,
                text: JSON.stringify(
                  {
                    status: "error",
                    error: errorMsg,
                  },
                  null,
                  2
                ),
              },
            ],
          };
        }

        // Trigger background update check (non-blocking).
        if (repoRoot) {
          maybeUpdateInBackground(repoRoot, config);
        }

        // Embed the query.
        const queryEmbedding = await embedQuery(parsed.query, config);

        // Search the database.
        const alpha = config.search?.alpha ?? 0.6;
        const results = searchDatabase(
          dbPath,
          queryEmbedding,
          parsed.query,
          parsed.maxResults,
          alpha
        );

        // Format results.
        const formatted = results.map((r) => {
          const m = r.metadata;
          let line = `${r.rank}. [${m["kind"]}] ${m["name"]} (${(r.similarity * 100).toFixed(1)}% match)`;
          if (m["signature"]) line += `\n   ${m["signature"]}`;
          if (m["file"] && m["line"] && m["line"] !== "0")
            line += `\n   ${m["file"]}:${m["line"]}`;
          else if (m["file"]) line += `\n   ${m["file"]}`;
          if (m["summary"]) line += `\n   ${m["summary"]}`;
          else if (m["doc"]) line += `\n   ${m["doc"].slice(0, 150)}`;
          return line;
        });

        return {
          content: [
            {
              type: "text" as const,
              text: JSON.stringify(
                {
                  status: "success",
                  query: parsed.query,
                  total_results: results.length,
                  results: formatted,
                },
                null,
                2
              ),
            },
          ],
        };
      } catch (error) {
        const message =
          error instanceof Error ? error.message : String(error);

        let hint = "";
        if (
          message.includes("credentials") ||
          message.includes("AccessDenied") ||
          message.includes("ExpiredToken") ||
          message.includes("security token")
        ) {
          hint =
            " Hint: If using Bedrock, run 'aws sso login' and ensure AWS_PROFILE is set." +
            " If using OpenAI, ensure your API key env var is set.";
        } else if (
          message.includes("ECONNREFUSED") ||
          message.includes("fetch failed")
        ) {
          hint =
            " Hint: If using Ollama, ensure it is running (`ollama serve`).";
        }

        return {
          content: [
            {
              type: "text" as const,
              text: JSON.stringify(
                { status: "error", error: message + hint },
                null,
                2
              ),
            },
          ],
        };
      }
    }
  );
}
