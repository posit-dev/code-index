# Hybrid Search: BM25 + Vector

## Problem

Vector-only search produces tight similarity scores between structurally
similar but semantically unrelated functions. For the query "scim provisioning":

| Result | Vector score | Relevant? |
|--------|-------------|-----------|
| isUserProvisioningEnabled | 63.8% | Yes |
| isCopilotAllowedByAdmin | 60.8% | No |
| startAgent | 60.5% | No |

A 3-point gap is insufficient for reliable ranking. The embedding model captures
structural patterns ("boolean feature-gating function") more than domain terms
("provisioning" vs "copilot"). Better summaries made this worse — accurate
descriptions of different functions now sound more alike than hallucinated ones.

## Solution

Add BM25 keyword search alongside vector search. Combine scores so exact term
matches boost relevant results while semantic similarity still handles
conceptual queries.

With hybrid scoring, the same query would produce:

| Result | Vector | BM25 | Hybrid (α=0.6) |
|--------|--------|------|-----------------|
| isUserProvisioningEnabled | 0.64 | ~0.9 | **0.74** |
| isCopilotAllowedByAdmin | 0.61 | ~0.0 | **0.36** |
| startAgent | 0.61 | ~0.0 | **0.36** |

In this theoretical example, the gap goes from 3 points to 38 points.

## Why hybrid, not just better embeddings

- Larger embedding models help but don't solve the structural similarity
  problem. Functions with identical signatures will always embed close together.
- BM25 is effectively free — SQLite FTS5 is built into the SQLite library
  already linked by code-index. No external service, no API calls, no latency.
- Hybrid search is the industry standard for production retrieval systems
  (Elasticsearch, Pinecone, Weaviate, Qdrant all offer it as a core feature).
- The two methods are complementary: vector catches "suspend" when the user
  says "pause"; BM25 catches "provisioning" when the user says "provisioning."

## Design

### Schema changes (vectorstore.go)

Add an FTS5 virtual table alongside the existing tables:

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS code_items_fts USING fts5(
    doc_id,
    content,
    kind,
    name,
    signature,
    file,
    receiver,
    package,
    summary,
    doc,
    content='code_items',
    content_rowid='id'
);
```

This is a *content-synced* FTS5 table — it reads content from `code_items`
rather than duplicating it. The `content=` and `content_rowid=` directives
tell FTS5 to use `code_items` as the backing store. This means:

- No extra disk space for text storage (FTS5 stores only its inverted index)
- Inserts/updates to `code_items` must be followed by matching FTS5 operations
- The `doc_id` column is included so we can join results back

### Populating the FTS index (vectorstore.go AddDocument)

After the existing upsert into `code_items`, add:

```sql
INSERT INTO code_items_fts(rowid, doc_id, content, kind, name, signature,
    file, receiver, package, summary, doc)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
```

For updates (ON CONFLICT path), delete the old FTS entry first:

```sql
DELETE FROM code_items_fts WHERE rowid = ?
```

This mirrors the existing delete-then-insert pattern used for `vec_items`.

### Hybrid search (vectorstore.go)

New method: `HybridSearch(ctx, queryEmbedding, queryText, maxResults, alpha)`

The method runs two queries and merges:

**Step 1 — Vector search (existing)**

Fetch top `maxResults * 3` candidates by vector similarity. This oversamples
to ensure good keyword matches aren't missed due to low vector rank.

```sql
SELECT v.rowid, v.distance, c.doc_id
FROM vec_items v
JOIN code_items c ON c.id = v.rowid
WHERE v.embedding MATCH ?
  AND k = ?
ORDER BY v.distance
```

**Step 2 — BM25 keyword search**

Fetch top `maxResults * 3` candidates by BM25 relevance:

```sql
SELECT c.id, c.doc_id, rank AS bm25_score
FROM code_items_fts
JOIN code_items c ON c.id = code_items_fts.rowid
WHERE code_items_fts MATCH ?
ORDER BY rank
LIMIT ?
```

FTS5 `rank` is a negative BM25 score (lower = more relevant). We negate
it for a positive score.

**Step 3 — Reciprocal Rank Fusion (RRF)**

We use RRF instead of score normalization. Weighted score combination
penalizes candidates that appear in only one result set — a BM25-only
match gets 0 for its missing vector score, capping its hybrid score at
`(1-α)`. This defeats the purpose of hybrid search, since BM25 is supposed
to rescue items the vector search misses entirely.

RRF is rank-based, not score-based, so missing entries simply contribute
nothing (no penalty). Each candidate's contribution from each list is
`1 / (k + rank)`, weighted by alpha:

```
const k = 60  // standard RRF constant

For each candidate in the union of both result sets:
    score = 0
    If candidate has vector rank r_v:
        score += α / (k + r_v)
    If candidate has BM25 rank r_b:
        score += (1 - α) / (k + r_b)
```

Sort by `score` descending, return top `maxResults`.

RRF scores are small numbers (~0.01–0.03). For display purposes, the
final scores are min-max normalized to 0–1 after ranking is decided —
this is purely cosmetic and doesn't affect result ordering.

**Step 4 — Fetch full metadata**

The top results are resolved back to full `code_items` rows for the response.

### Alpha parameter and opting out

Alpha controls the blend: `hybrid_score = α * vector + (1 - α) * keyword`.

```json
{
  "search": {
    "alpha": 0.6
  }
}
```

- `alpha = 0.6` (default): hybrid search — 60% vector, 40% keyword
- `alpha = 1.0`: vector-only — BM25 query is skipped entirely
- Any value in between adjusts the balance

Default is 0.6. Rationale: vector search should still dominate for conceptual
queries ("how does authentication work") where no exact keyword matches exist.
BM25 is the tiebreaker that separates structurally similar results.

The FTS5 table is always built regardless of alpha — it's negligible overhead
(an inverted index with no data duplication) and avoids requiring a rebuild if
someone later enables hybrid search. The `HybridSearch` method short-circuits
when `alpha >= 1.0`:

```go
if alpha >= 1.0 {
    return vs.Search(ctx, queryEmbedding, maxResults)
}
```

This keeps one code path, one schema, and the decision is purely a search-time
parameter. No codebase is forced into hybrid — they just get it by default.

### FTS5 query construction

The user's natural language query needs sanitization and transformation for FTS5:

1. Strip FTS5 special characters (`*`, `"`, `(`, `)`, `{`, `}`, `+`, `-`)
2. Remove FTS5 reserved keywords (`AND`, `OR`, `NOT`, `NEAR`) when they
   appear as standalone tokens
3. Split remaining text into individual terms
4. Drop empty terms
5. Join with `OR` so documents matching *any* query term are candidates —
   BM25 naturally ranks documents with more matching terms higher
6. If no valid terms remain after sanitization, skip the BM25 query entirely

No stemming configuration needed — FTS5's default tokenizer handles English.

Example: `"scim provisioning"` becomes the FTS5 query `scim OR provisioning`
(matches documents containing either term; documents with both rank highest).

Using OR rather than AND (FTS5's default) avoids the failure mode where a
three-term query like "scim user provisioning" returns zero BM25 results
because no single document contains all three terms. Since vector search
handles broad recall, BM25's role is to boost exact-term hits — OR maximizes
that signal. If OR proves too noisy in practice, switching to AND is a
one-line change.

For queries with no keyword matches (pure conceptual), BM25 returns no results
and the hybrid score falls back to vector-only — which is the correct behavior.

### API changes

**VectorStore** gains one new public method:

```go
func (vs *VectorStore) HybridSearch(
    ctx context.Context,
    queryEmbedding []float32,
    queryText string,
    maxResults int,
    alpha float64,
) ([]SearchResult, error)
```

The existing `Search` method is unchanged for backward compatibility.

**SearchResult** gains a `Score` field alongside the existing `Similarity`:

```go
type SearchResult struct {
    ID         string
    Content    string
    Similarity float32           // vector similarity (preserved for compat)
    Score      float32           // hybrid score (when using HybridSearch)
    Metadata   map[string]string
}
```

### CLI changes (search.go)

`runSearch` calls `HybridSearch` instead of `Search`, passing the raw query
text alongside the embedding. The displayed score uses `Score` (hybrid) instead
of `Similarity` (vector-only).

### MCP server changes (code-search.ts)

`searchDatabase` adds the FTS5 query alongside the vector query, implements
the same normalize-and-combine logic in TypeScript. The query text is already
available (it's the tool's input parameter).

Changes to `searchDatabase`:
1. Run vector query (existing)
2. Run FTS5 BM25 query (new)
3. Merge and normalize scores
4. Return combined results

The response format stays the same — only the ranking changes.

### Schema migration

Existing databases lack the FTS5 table. On `OpenVectorStore`, check if
`code_items_fts` exists:

```sql
SELECT name FROM sqlite_master
WHERE type='table' AND name='code_items_fts'
```

If missing, create it and populate from existing `code_items`:

```sql
INSERT INTO code_items_fts(rowid, doc_id, content, kind, name, signature,
    file, receiver, package, summary, doc)
SELECT id, doc_id, content, kind, name, signature,
    file, receiver, package, summary, doc
FROM code_items
```

This is a one-time migration that runs automatically. No `--reset` required.

## Files changed

| File | Change |
|------|--------|
| `indexer/config.go` | Add `SearchConfig` with `Alpha` field, wire into `IndexConfig` |
| `indexer/vectorstore.go` | FTS5 table creation, AddDocument FTS insert, HybridSearch method, migration |
| `cmd/code-index/cmd/search.go` | Read alpha from config, call HybridSearch, pass query text |
| `mcp/src/code-search.ts` | Read alpha from config, FTS5 query, score merging, same normalize logic |

No changes to: parsing, generation, embedding, build, types.

## What this does NOT change

- The embedding pipeline (parse → generate → build → embed) is untouched
- The embedding text and model are unchanged
- The vector similarity scores are still computed and available
- Existing databases are auto-migrated (no rebuild needed)
- The `Search` method still works for callers that don't need hybrid

## Verification

1. **Unit test**: Create a VectorStore with known documents, run HybridSearch
   with a query that has exact term matches — verify keyword-matching results
   rank higher than structurally-similar non-matches.

2. **Integration test**: Re-run the "scim provisioning" query against the
   rstudio open-source index after migration. Verify:
   - isUserProvisioningEnabled ranks #1 with clear separation
   - Copilot/assistant functions drop significantly
   - Conceptual queries ("how does session management work") still return
     good results (vector component dominates when no keyword matches)

3. **Regression test**: Run a query with no keyword matches in any document
   ("abstract concept about code organization") — verify results are identical
   to vector-only search (BM25 contributes nothing, alpha weighting degrades
   gracefully).

4. **Normalization edge case**: Search with a query that produces identical
   vector scores for all candidates — verify the `max == min` guard prevents
   division-by-zero and results still rank correctly by BM25 signal alone.

5. **Dual-implementation parity**: The normalize-and-combine logic exists in
   both Go (`vectorstore.go`) and TypeScript (`code-search.ts`). Run the
   same query through both the CLI (`code-index search`) and MCP server
   (`code_search` tool) against the same database and verify identical
   ranking order. Include edge cases: empty BM25 results, single result,
   all-identical vector scores.

## Cost

Zero ongoing cost. FTS5 is built into SQLite. No API calls, no external
services. The FTS5 inverted index adds modest disk space (~10-20% of the
existing DB size). Query latency adds one SQLite query (~1ms).
