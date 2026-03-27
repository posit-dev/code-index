// Copyright (C) 2026 by Posit Software, PBC
// Licensed under the MIT License. See LICENSE for details.

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";
import { execSync } from "child_process";
import { resolve } from "path";
import { existsSync, readFileSync } from "fs";
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
  aws?: {
    region?: string;
  };
}

/**
 * Find the code-index.db by searching upward from cwd.
 */
function findDatabase(cwd: string): string | null {
  let dir = cwd;
  while (dir !== "/") {
    const candidate = resolve(dir, ".code-index", "code-index.db");
    if (existsSync(candidate)) return candidate;
    dir = resolve(dir, "..");
  }
  return null;
}

/**
 * Find the repo root (directory containing .code-index.json).
 */
function findRepoRoot(cwd: string): string | null {
  let dir = cwd;
  while (dir !== "/") {
    if (existsSync(resolve(dir, ".code-index.json"))) return dir;
    dir = resolve(dir, "..");
  }
  return null;
}

/**
 * Load config from .code-index.json.
 */
function loadConfig(repoRoot: string): CodeIndexConfig {
  const configPath = resolve(repoRoot, ".code-index.json");
  if (!existsSync(configPath)) return {};
  try {
    return JSON.parse(readFileSync(configPath, "utf-8")) as CodeIndexConfig;
  } catch {
    return {};
  }
}

/**
 * Embed a query using the OpenAI-compatible embeddings API.
 * Works with OpenAI, Ollama, Together AI, LM Studio, vLLM, etc.
 */
async function embedQueryOpenAI(
  query: string,
  config: CodeIndexConfig
): Promise<number[]> {
  const baseURL = (config.embeddings?.base_url || "https://api.openai.com/v1").replace(/\/+$/, "");
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

/**
 * Embed a query using Cohere Embed v4 via AWS Bedrock.
 */
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

/**
 * Embed a query using the configured provider.
 */
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

/**
 * Search the sqlite-vec database for similar vectors.
 */
function searchDatabase(
  dbPath: string,
  queryEmbedding: number[],
  maxResults: number
): Array<{
  rank: number;
  similarity: number;
  metadata: Record<string, string>;
}> {
  const db = new Database(dbPath, { readonly: true });
  sqliteVec.load(db);

  try {
    const stmt = db.prepare(`
      SELECT v.rowid, v.distance,
        c.doc_id, c.kind, c.name, c.signature,
        c.file, c.line, c.receiver, c.package, c.summary, c.doc
      FROM vec_items v
      JOIN code_items c ON c.id = v.rowid
      WHERE v.embedding MATCH ?
        AND k = ?
      ORDER BY v.distance
    `);

    const rows = stmt.all(
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
  } finally {
    db.close();
  }
}

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

        // Find the database.
        let dbPath = findDatabase(cwd);
        if (!dbPath) {
          // Try pulling from S3.
          const repoRoot = findRepoRoot(cwd);
          if (repoRoot) {
            const pullScript = resolve(
              repoRoot,
              "scripts",
              "pull-code-index-vectors.sh"
            );
            if (existsSync(pullScript)) {
              try {
                execSync(pullScript + " --quiet", {
                  cwd: repoRoot,
                  timeout: 60000,
                  env: { ...process.env },
                });
                dbPath = findDatabase(cwd);
              } catch {
                // Pull failed
              }
            }
          }
        }

        if (!dbPath) {
          return {
            content: [
              {
                type: "text" as const,
                text: JSON.stringify(
                  {
                    status: "error",
                    error:
                      "Vector database not found. Run 'code-index all' to build the index, " +
                      "or ensure .code-index/code-index.db exists.",
                  },
                  null,
                  2
                ),
              },
            ],
          };
        }

        // Load config.
        const repoRoot = findRepoRoot(cwd);
        const config = repoRoot ? loadConfig(repoRoot) : {};

        // Embed the query.
        const queryEmbedding = await embedQuery(parsed.query, config);

        // Search the database.
        const results = searchDatabase(
          dbPath,
          queryEmbedding,
          parsed.maxResults
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
