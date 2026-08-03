package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// EmbeddingDimensions is the width of the vectors the embedding sidecar
// produces (bge-small-en-v1.5). It must match VECTOR(...) in
// schema/article_embeddings.sql: MariaDB rejects an insert of any other length,
// so a model swap is a schema change, not a config change.
const EmbeddingDimensions = 384

// EmbeddingSourceMaxChars caps how much of an article body gets embedded. The
// model truncates at its own context window regardless; cutting here keeps the
// hash stable against edits far past the point the model ever saw, so a typo fix
// in the last paragraph of a long feature does not queue a pointless re-embed.
const EmbeddingSourceMaxChars = 5000

// EnsureArticleEmbeddingsTable creates the vector table the CMS now owns.
//
// This table used to be created (and dropped) by the WordPress ETL, which meant
// every reseed wiped it and nothing ever refilled it for articles written in the
// CMS afterwards. Ownership moving here is what lets the reconciler treat a
// missing row as work to do rather than as the normal state of the world.
//
// Callers must treat a failure as non-fatal: VECTOR requires MariaDB 11.7+, and
// a CMS pointed at an older database should lose semantic search rather than
// refuse to boot.
func EnsureArticleEmbeddingsTable(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, TableSchema("article_embeddings")); err != nil {
		return err
	}

	// Expand-only migration for databases seeded by the old ETL, whose table has
	// article_id and embedding but none of the bookkeeping columns. Without
	// source_hash those rows read as "hash mismatch" and the reconciler re-embeds
	// the entire corpus once, which is correct but slow -- so they default to ''
	// and ArticlesNeedingEmbeddings treats '' as "unknown, leave it alone".
	_, err := conn.ExecContext(ctx, `
		ALTER TABLE article_embeddings
		ADD COLUMN IF NOT EXISTS source_hash CHAR(64) NOT NULL DEFAULT '',
		ADD COLUMN IF NOT EXISTS model VARCHAR(128) NOT NULL DEFAULT '',
		ADD COLUMN IF NOT EXISTS updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	`)
	return err
}

// EmbeddingSource is one article's text to embed, paired with the hash that
// decides whether the stored vector is still current.
type EmbeddingSource struct {
	ArticleID int64
	Text      string
	Hash      string
}

// BuildEmbeddingSource assembles the text a single article is embedded from and
// hashes it. Title and tags lead because they carry disproportionate signal for
// retrieval, and because a headline rewrite should invalidate the vector even
// when the body is untouched.
//
// IMPORTANT: the WordPress ETL bulk-loads embeddings for the migrated archive
// and writes the same hash, so its ArticleEmbeddingsFormatter._embedding_source
// must produce byte-identical text for the same article. If the two drift, the
// ETL's rows read as permanently stale and the reconciler re-embeds the entire
// archive after every reseed. Change both or neither.
func BuildEmbeddingSource(articleID int64, title, tags, body string) EmbeddingSource {
	parts := make([]string, 0, 3)
	for _, part := range []string{title, tags, stripHTMLForEmbedding(body)} {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	// Truncate by rune, not byte: the ETL counts characters, and a byte cut would
	// also split a multi-byte character mid-sequence.
	text := strings.Join(parts, "\n\n")
	if runes := []rune(text); len(runes) > EmbeddingSourceMaxChars {
		text = string(runes[:EmbeddingSourceMaxChars])
	}

	sum := sha256.Sum256([]byte(text))
	return EmbeddingSource{ArticleID: articleID, Text: text, Hash: hex.EncodeToString(sum[:])}
}

// stripHTMLForEmbedding reduces body HTML to the prose the model should see.
// Left in, markup burns a real fraction of a 512-token context window on tag
// names, and embeds two articles as similar because they share a shortcode.
func stripHTMLForEmbedding(body string) string {
	text := htmlTagPattern.ReplaceAllString(body, " ")
	text = html.UnescapeString(text)
	return strings.Join(strings.Fields(text), " ")
}

// ArticlesNeedingEmbeddings returns up to limit live articles whose stored
// vector is missing or stale, oldest-published last so a reseeded corpus fills
// in newest-first and search quality recovers where it is most noticed.
//
// A row with an empty source_hash is left alone: those came from the ETL bulk
// load, which does not record hashes, and re-embedding them on that basis would
// mean re-embedding the whole archive on every deploy.
func ArticlesNeedingEmbeddings(ctx context.Context, conn *sql.DB, model string, limit int) ([]EmbeddingSource, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than 0")
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT a.`+"`id`"+`, a.`+"`title`"+`, a.`+"`tags`"+`, a.`+"`text`"+`
		FROM articles AS a
		LEFT JOIN article_embeddings AS e ON e.article_id = a.id
		WHERE a.`+"`pub_date`"+` IS NOT NULL
		  AND a.`+"`pub_date`"+` <= UTC_TIMESTAMP()
		  AND a.`+"`archived_at`"+` IS NULL
		  AND (e.article_id IS NULL OR (e.source_hash <> '' AND e.model <> ? ))
		ORDER BY a.`+"`pub_date`"+` DESC, a.`+"`id`"+` DESC
		LIMIT `+strconv.Itoa(limit), model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []EmbeddingSource
	for rows.Next() {
		var id int64
		var title, tags, body sql.NullString
		if err := rows.Scan(&id, &title, &tags, &body); err != nil {
			return nil, err
		}
		sources = append(sources, BuildEmbeddingSource(id, title.String, tags.String, body.String))
	}
	return sources, rows.Err()
}

// StaleEmbeddingArticles returns articles whose text has changed since it was
// embedded. This is separate from ArticlesNeedingEmbeddings because it has to
// hash every candidate's current text to compare, so it is the expensive half
// and runs on a slower cadence.
func StaleEmbeddingArticles(ctx context.Context, conn *sql.DB, limit int) ([]EmbeddingSource, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than 0")
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT a.`+"`id`"+`, a.`+"`title`"+`, a.`+"`tags`"+`, a.`+"`text`"+`, e.source_hash
		FROM articles AS a
		JOIN article_embeddings AS e ON e.article_id = a.id
		WHERE a.`+"`pub_date`"+` IS NOT NULL
		  AND a.`+"`pub_date`"+` <= UTC_TIMESTAMP()
		  AND a.`+"`archived_at`"+` IS NULL
		  AND e.source_hash <> ''
		  AND a.`+"`mod_date`"+` >= e.updated_at
		ORDER BY a.`+"`mod_date`"+` DESC
		LIMIT `+strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []EmbeddingSource
	for rows.Next() {
		var id int64
		var title, tags, body sql.NullString
		var storedHash string
		if err := rows.Scan(&id, &title, &tags, &body, &storedHash); err != nil {
			return nil, err
		}
		source := BuildEmbeddingSource(id, title.String, tags.String, body.String)
		// mod_date only says the row was touched; the hash says the embedded text
		// actually differs. Most saves change neither title, tags, nor body.
		if source.Hash == storedHash {
			continue
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// SaveArticleEmbedding upserts one vector.
func SaveArticleEmbedding(ctx context.Context, conn *sql.DB, articleID int64, vector []float32, hash, model string) error {
	if len(vector) != EmbeddingDimensions {
		return fmt.Errorf("embedding has %d dimensions, want %d", len(vector), EmbeddingDimensions)
	}

	_, err := conn.ExecContext(ctx, `
		INSERT INTO article_embeddings (article_id, embedding, source_hash, model, updated_at)
		VALUES (?, VEC_FromText(?), ?, ?, UTC_TIMESTAMP())
		ON DUPLICATE KEY UPDATE
			embedding = VALUES(embedding),
			source_hash = VALUES(source_hash),
			model = VALUES(model),
			updated_at = UTC_TIMESTAMP()
	`, articleID, FormatVector(vector), hash, model)
	return err
}

// DeleteOrphanedEmbeddings drops vectors for articles that no longer exist or
// are no longer live. Without it an archived article keeps answering vector
// queries, since the nearest-neighbour scan runs before the visibility join.
func DeleteOrphanedEmbeddings(ctx context.Context, conn *sql.DB) (int64, error) {
	result, err := conn.ExecContext(ctx, `
		DELETE e FROM article_embeddings AS e
		LEFT JOIN articles AS a ON a.id = e.article_id
		WHERE a.id IS NULL
		   OR a.`+"`archived_at`"+` IS NOT NULL
		   OR a.`+"`pub_date`"+` IS NULL
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// FormatVector renders a vector in the bracketed form VEC_FromText parses.
func FormatVector(vector []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vector {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', 8, 32))
	}
	b.WriteByte(']')
	return b.String()
}
