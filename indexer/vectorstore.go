// Copyright (C) 2026 by Posit Software, PBC
package indexer

// #cgo CFLAGS: -I${SRCDIR}/../vendor/github.com/mattn/go-sqlite3
import "C"

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func init() {
	sqlite_vec.Auto()
}

// VectorStore manages the persistent vector database for code search.
type VectorStore struct {
	db     *sql.DB
	dbPath string
}

// OpenVectorStore opens or creates the vector database at the given path.
func OpenVectorStore(outputDir string) (*VectorStore, error) {
	dbPath := filepath.Join(outputDir, "code-index.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening vector database: %w", err)
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	return &VectorStore{
		db:     db,
		dbPath: dbPath,
	}, nil
}

func initSchema(db *sql.DB) error {
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
		)`, EmbeddingDimensions),
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
	defer rows.Close()

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

// Count returns the number of documents in the store.
func (vs *VectorStore) Count() int {
	var count int
	_ = vs.db.QueryRow("SELECT COUNT(*) FROM code_items").Scan(&count) //nolint:errcheck
	return count
}

// Reset deletes and recreates the database.
func (vs *VectorStore) Reset(ctx context.Context) error {
	vs.db.Close()

	if err := os.Remove(vs.dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing database: %w", err)
	}

	db, err := sql.Open("sqlite3", vs.dbPath)
	if err != nil {
		return fmt.Errorf("recreating database: %w", err)
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return fmt.Errorf("reinitializing schema: %w", err)
	}

	vs.db = db
	return nil
}

// Close closes the database connection.
func (vs *VectorStore) Close() error {
	return vs.db.Close()
}
