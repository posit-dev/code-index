// Copyright (C) 2026 by Posit Software, PBC
package indexer

// #cgo CFLAGS: -I${SRCDIR}/../vendor/github.com/mattn/go-sqlite3
import "C"

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

// VectorStore manages the persistent vector database for code search.
type VectorStore struct {
	db         *sql.DB
	dbPath     string
	dimensions int
}

// OpenVectorStore opens or creates the vector database at the given path.
// dimensions is the embedding vector size (e.g., 1536 for Cohere, 768 for nomic-embed-text).
// If dimensions is 0 and the database already exists, the stored dimension is used.
// If dimensions is non-zero and differs from the stored value, an error is returned.
func OpenVectorStore(outputDir string, dimensions int) (*VectorStore, error) {
	dbPath := filepath.Join(outputDir, "code-index.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening vector database: %w", err)
	}

	// Close db on any error after this point.
	success := false
	defer func() {
		if !success {
			if cerr := db.Close(); cerr != nil {
				log.Printf("warning: closing database after error: %v", cerr)
			}
		}
	}()

	// Create the metadata table first (always safe).
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("creating metadata table: %w", err)
	}

	// Check for stored dimensions.
	storedDims := 0
	var storedStr string
	err = db.QueryRow("SELECT value FROM metadata WHERE key = 'embedding_dimensions'").Scan(&storedStr)
	if err == nil {
		storedDims, _ = strconv.Atoi(storedStr)
	}

	if dimensions == 0 {
		dimensions = storedDims
	}
	if dimensions == 0 {
		return nil, fmt.Errorf("embedding dimensions unknown — run 'code-index embed' to build the database")
	}

	// Validate dimension consistency.
	if storedDims > 0 && dimensions != storedDims {
		return nil, fmt.Errorf("embedding dimensions changed (was %d, now %d) — run with --reset to rebuild the database", storedDims, dimensions)
	}

	if err := initSchema(db, dimensions); err != nil {
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	if err := backfillFTS(db); err != nil {
		return nil, fmt.Errorf("backfilling FTS index: %w", err)
	}

	// Store the dimensions.
	if _, err := db.Exec(`INSERT INTO metadata (key, value) VALUES ('embedding_dimensions', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		strconv.Itoa(dimensions)); err != nil {
		return nil, fmt.Errorf("storing embedding dimensions: %w", err)
	}

	success = true
	return &VectorStore{
		db:         db,
		dbPath:     dbPath,
		dimensions: dimensions,
	}, nil
}

// Dimensions returns the embedding dimension size for this store.
func (vs *VectorStore) Dimensions() int {
	return vs.dimensions
}

func initSchema(db *sql.DB, dimensions int) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS code_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			doc_id TEXT UNIQUE NOT NULL,
			content TEXT NOT NULL,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			signature TEXT,
			file TEXT,
			line INTEGER,
			receiver TEXT,
			package TEXT,
			summary TEXT,
			doc TEXT
		)`,
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_items USING vec0(
			embedding float[%d] distance_metric=cosine
		)`, dimensions),
		`CREATE VIRTUAL TABLE IF NOT EXISTS code_items_fts USING fts5(
			doc_id,
			content,
			kind,
			name,
			signature,
			file,
			receiver,
			package,
			summary,
			doc
		)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("executing %q: %w", stmt[:60], err)
		}
	}
	return nil
}

// DocumentMetadata holds the metadata stored alongside each vector.
type DocumentMetadata struct {
	Kind      string // "function", "type", "file", "package"
	Name      string
	Signature string
	File      string
	Line      int
	Receiver  string
	Package   string
	Summary   string
	Doc       string
}

// AddDocument adds a single document with a pre-computed embedding.
func (vs *VectorStore) AddDocument(ctx context.Context, id, content string, embedding []float32, meta DocumentMetadata) error {
	tx, err := vs.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck

	// Truncate long docs.
	doc := meta.Doc
	if len(doc) > 500 {
		doc = doc[:500] + "..."
	}

	// Upsert metadata.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO code_items (doc_id, content, kind, name, signature, file, line, receiver, package, summary, doc)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(doc_id) DO UPDATE SET
			content=excluded.content, kind=excluded.kind, name=excluded.name,
			signature=excluded.signature, file=excluded.file, line=excluded.line,
			receiver=excluded.receiver, package=excluded.package,
			summary=excluded.summary, doc=excluded.doc
	`, id, content, meta.Kind, meta.Name, meta.Signature, meta.File, meta.Line,
		meta.Receiver, meta.Package, meta.Summary, doc)
	if err != nil {
		return fmt.Errorf("inserting metadata: %w", err)
	}

	// Get the actual rowid (LastInsertId is unreliable after ON CONFLICT UPDATE).
	var rowID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM code_items WHERE doc_id = ?", id).Scan(&rowID)
	if err != nil {
		return fmt.Errorf("getting row ID: %w", err)
	}

	// Delete old FTS entry (best-effort, mirrors vec_items pattern).
	_, _ = tx.ExecContext(ctx,
		"DELETE FROM code_items_fts WHERE rowid = ?", rowID)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO code_items_fts(rowid, doc_id, content, kind, name,
			signature, file, receiver, package, summary, doc)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rowID, id, content, meta.Kind, meta.Name, meta.Signature,
		meta.File, meta.Receiver, meta.Package, meta.Summary, doc)
	if err != nil {
		return fmt.Errorf("inserting FTS entry: %w", err)
	}

	// Delete existing vector if present, then insert.
	// sqlite-vec virtual tables don't support INSERT OR REPLACE.
	vecJSON, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("marshaling embedding: %w", err)
	}

	_, _ = tx.ExecContext(ctx, "DELETE FROM vec_items WHERE rowid = ?", rowID) //nolint:errcheck // best-effort delete before insert
	_, err = tx.ExecContext(ctx, `
		INSERT INTO vec_items (rowid, embedding)
		VALUES (?, ?)
	`, rowID, string(vecJSON))
	if err != nil {
		return fmt.Errorf("inserting vector: %w", err)
	}

	return tx.Commit()
}

// SearchResult is a single result from a vector search.
type SearchResult struct {
	ID         string
	Content    string
	Similarity float32
	Score      float32
	Metadata   map[string]string
}

// Search finds the most similar documents to the given embedding.
func (vs *VectorStore) Search(ctx context.Context, queryEmbedding []float32, maxResults int) ([]SearchResult, error) {
	vecJSON, err := json.Marshal(queryEmbedding)
	if err != nil {
		return nil, fmt.Errorf("marshaling query: %w", err)
	}

	rows, err := vs.db.QueryContext(ctx, `
		SELECT v.rowid, v.distance,
			c.doc_id, c.content, c.kind, c.name, c.signature,
			c.file, c.line, c.receiver, c.package, c.summary, c.doc
		FROM vec_items v
		JOIN code_items c ON c.id = v.rowid
		WHERE v.embedding MATCH ?
		  AND k = ?
		ORDER BY v.distance
	`, string(vecJSON), maxResults)
	if err != nil {
		return nil, fmt.Errorf("querying vectors: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("warning: closing rows: %v", cerr)
		}
	}()

	var results []SearchResult
	for rows.Next() {
		var (
			rowid                                                    int64
			distance                                                 float64
			docID, content, kind, name, file, receiver, pkg, summary string
			signature, doc                                           sql.NullString
			line                                                     int
		)

		err := rows.Scan(&rowid, &distance, &docID, &content, &kind, &name, &signature,
			&file, &line, &receiver, &pkg, &summary, &doc)
		if err != nil {
			return nil, fmt.Errorf("scanning result: %w", err)
		}

		// Convert cosine distance to similarity (1 - distance).
		similarity := float32(1.0 - distance)
		if similarity < 0 {
			similarity = 0
		}
		if math.IsNaN(float64(similarity)) {
			similarity = 0
		}

		metadata := map[string]string{
			"kind": kind,
			"name": name,
			"file": file,
			"line": strconv.Itoa(line),
		}
		if signature.Valid && signature.String != "" {
			metadata["signature"] = signature.String
		}
		if receiver != "" {
			metadata["receiver"] = receiver
		}
		if pkg != "" {
			metadata["package"] = pkg
		}
		if summary != "" {
			metadata["summary"] = summary
		}
		if doc.Valid && doc.String != "" {
			metadata["doc"] = doc.String
		}

		results = append(results, SearchResult{
			ID:         docID,
			Content:    content,
			Similarity: similarity,
			Metadata:   metadata,
		})
	}

	return results, rows.Err()
}

// backfillFTS populates the FTS5 index from code_items when the FTS table
// exists but is empty (e.g., after upgrading an older database).
func backfillFTS(db *sql.DB) error {
	var itemCount, ftsCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM code_items").Scan(&itemCount); err != nil {
		return err
	}
	if itemCount == 0 {
		return nil
	}
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM code_items_fts").Scan(&ftsCount); err != nil {
		return err
	}
	if ftsCount > 0 {
		return nil
	}
	_, err := db.Exec(`
		INSERT INTO code_items_fts(rowid, doc_id, content, kind, name,
			signature, file, receiver, package, summary, doc)
		SELECT id, doc_id, content, kind, name, signature,
			file, receiver, package, summary, doc
		FROM code_items`)
	return err
}

func sanitizeFTS5Query(query string) string {
	reserved := map[string]bool{
		"AND": true, "OR": true, "NOT": true, "NEAR": true,
	}
	special := `*"(){}+-`
	terms := strings.Fields(query)
	var clean []string
	for _, t := range terms {
		var b strings.Builder
		for _, ch := range t {
			if !strings.ContainsRune(special, ch) {
				b.WriteRune(ch)
			}
		}
		s := b.String()
		if s == "" || reserved[strings.ToUpper(s)] {
			continue
		}
		clean = append(clean, s)
	}
	if len(clean) == 0 {
		return ""
	}
	return strings.Join(clean, " OR ")
}

func normalizeScores(
	scores map[string]float64,
) map[string]float64 {
	if len(scores) == 0 {
		return scores
	}
	minVal, maxVal := math.Inf(1), math.Inf(-1)
	for _, v := range scores {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}
	out := make(map[string]float64, len(scores))
	for k, v := range scores {
		if maxVal == minVal {
			out[k] = 1.0
		} else {
			out[k] = (v - minVal) / (maxVal - minVal)
		}
	}
	return out
}

func (vs *VectorStore) fetchVectorCandidates(
	ctx context.Context,
	queryEmbedding []float32,
	limit int,
) (map[string]float64, error) {
	vecJSON, err := json.Marshal(queryEmbedding)
	if err != nil {
		return nil, fmt.Errorf("marshaling query: %w", err)
	}
	rows, err := vs.db.QueryContext(ctx, `
		SELECT c.doc_id, v.distance
		FROM vec_items v
		JOIN code_items c ON c.id = v.rowid
		WHERE v.embedding MATCH ?
		  AND k = ?
		ORDER BY v.distance
	`, string(vecJSON), limit)
	if err != nil {
		return nil, fmt.Errorf("querying vectors: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("warning: closing rows: %v", cerr)
		}
	}()
	scores := make(map[string]float64)
	for rows.Next() {
		var docID string
		var distance float64
		if err := rows.Scan(&docID, &distance); err != nil {
			return nil, fmt.Errorf("scanning vector result: %w", err)
		}
		sim := 1.0 - distance
		if sim < 0 {
			sim = 0
		}
		if math.IsNaN(sim) {
			sim = 0
		}
		scores[docID] = sim
	}
	return scores, rows.Err()
}

func (vs *VectorStore) fetchBM25Candidates(
	ctx context.Context,
	queryText string,
	limit int,
) (map[string]float64, error) {
	sanitized := sanitizeFTS5Query(queryText)
	if sanitized == "" {
		return nil, nil
	}
	rows, err := vs.db.QueryContext(ctx, `
		SELECT c.doc_id, code_items_fts.rank AS bm25_score
		FROM code_items_fts
		JOIN code_items c ON c.id = code_items_fts.rowid
		WHERE code_items_fts MATCH ?
		ORDER BY code_items_fts.rank
		LIMIT ?
	`, sanitized, limit)
	if err != nil {
		return nil, fmt.Errorf("querying FTS: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("warning: closing rows: %v", cerr)
		}
	}()
	scores := make(map[string]float64)
	for rows.Next() {
		var docID string
		var bm25Score float64
		if err := rows.Scan(&docID, &bm25Score); err != nil {
			return nil, fmt.Errorf("scanning FTS result: %w", err)
		}
		// FTS5 rank is negative (lower = more relevant); negate for positive scores.
		scores[docID] = -bm25Score
	}
	return scores, rows.Err()
}

func (vs *VectorStore) fetchMetadataByDocIDs(
	ctx context.Context,
	docIDs []string,
) (map[string]SearchResult, error) {
	if len(docIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(docIDs))
	args := make([]any, len(docIDs))
	for i, id := range docIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT doc_id, content, kind, name, signature,
			file, line, receiver, package, summary, doc
		FROM code_items
		WHERE doc_id IN (%s)
	`, strings.Join(placeholders, ","))
	rows, err := vs.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("fetching metadata: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("warning: closing rows: %v", cerr)
		}
	}()
	results := make(map[string]SearchResult, len(docIDs))
	for rows.Next() {
		var (
			docID, content, kind, name string
			file, receiver, pkg, summary string
			signature, doc               sql.NullString
			line                         int
		)
		err := rows.Scan(&docID, &content, &kind, &name,
			&signature, &file, &line, &receiver, &pkg,
			&summary, &doc)
		if err != nil {
			return nil, fmt.Errorf("scanning metadata: %w", err)
		}
		meta := map[string]string{
			"kind": kind,
			"name": name,
			"file": file,
			"line": strconv.Itoa(line),
		}
		if signature.Valid && signature.String != "" {
			meta["signature"] = signature.String
		}
		if receiver != "" {
			meta["receiver"] = receiver
		}
		if pkg != "" {
			meta["package"] = pkg
		}
		if summary != "" {
			meta["summary"] = summary
		}
		if doc.Valid && doc.String != "" {
			meta["doc"] = doc.String
		}
		results[docID] = SearchResult{
			ID:       docID,
			Content:  content,
			Metadata: meta,
		}
	}
	return results, rows.Err()
}

// HybridSearch combines vector similarity with BM25 keyword matching.
// Alpha controls the balance: 1.0 = pure vector, 0.0 = pure BM25.
func (vs *VectorStore) HybridSearch(
	ctx context.Context,
	queryEmbedding []float32,
	queryText string,
	maxResults int,
	alpha float64,
) ([]SearchResult, error) {
	if alpha >= 1.0 {
		return vs.Search(ctx, queryEmbedding, maxResults)
	}

	expandedLimit := maxResults * 3
	vecScores, err := vs.fetchVectorCandidates(
		ctx, queryEmbedding, expandedLimit)
	if err != nil {
		return nil, err
	}

	bm25Scores, err := vs.fetchBM25Candidates(
		ctx, queryText, expandedLimit)
	if err != nil {
		return nil, err
	}

	vecNorm := normalizeScores(vecScores)
	bm25Norm := normalizeScores(bm25Scores)

	candidates := make(map[string]bool)
	for id := range vecNorm {
		candidates[id] = true
	}
	for id := range bm25Norm {
		candidates[id] = true
	}

	type rankedDoc struct {
		docID       string
		hybridScore float64
		vecSim      float64
	}
	rl := make([]rankedDoc, 0, len(candidates))
	for id := range candidates {
		vn := vecNorm[id]
		bn := bm25Norm[id]
		h := alpha*vn + (1-alpha)*bn
		rl = append(rl, rankedDoc{id, h, vecScores[id]})
	}
	sort.Slice(rl, func(i, j int) bool {
		return rl[i].hybridScore > rl[j].hybridScore
	})
	if len(rl) > maxResults {
		rl = rl[:maxResults]
	}

	docIDs := make([]string, len(rl))
	for i, r := range rl {
		docIDs[i] = r.docID
	}
	metaMap, err := vs.fetchMetadataByDocIDs(ctx, docIDs)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(rl))
	for _, r := range rl {
		sr := metaMap[r.docID]
		sr.Score = float32(r.hybridScore)
		sr.Similarity = float32(r.vecSim)
		results = append(results, sr)
	}
	return results, nil
}

// Count returns the number of documents in the store.
func (vs *VectorStore) Count() int {
	var count int
	_ = vs.db.QueryRow("SELECT COUNT(*) FROM code_items").Scan(&count) //nolint:errcheck
	return count
}

// Reset deletes and recreates the database.
func (vs *VectorStore) Reset(ctx context.Context) error {
	dims := vs.dimensions
	if err := vs.db.Close(); err != nil {
		log.Printf("warning: closing database before reset: %v", err)
	}

	if err := os.Remove(vs.dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing database: %w", err)
	}

	db, err := sql.Open("sqlite3", vs.dbPath)
	if err != nil {
		return fmt.Errorf("recreating database: %w", err)
	}

	// Close db on any error after this point.
	success := false
	defer func() {
		if !success {
			if cerr := db.Close(); cerr != nil {
				log.Printf("warning: closing database after reset error: %v", cerr)
			}
		}
	}()

	// Recreate metadata table and store dimensions.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("creating metadata table: %w", err)
	}

	if err := initSchema(db, dims); err != nil {
		return fmt.Errorf("reinitializing schema: %w", err)
	}

	if _, err := db.Exec(`INSERT INTO metadata (key, value) VALUES ('embedding_dimensions', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		strconv.Itoa(dims)); err != nil {
		return fmt.Errorf("storing embedding dimensions: %w", err)
	}

	success = true
	vs.db = db
	return nil
}

// Close closes the database connection.
func (vs *VectorStore) Close() error {
	return vs.db.Close()
}
