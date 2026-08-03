package embeddings

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	db "server/internal/database"
)

// Reconciler keeps article_embeddings in step with articles.
//
// This is a background loop rather than a write hook on PatchArticle/PutArticle/
// PostArticles for three reasons. There are three write paths and a bulk ETL
// load, and a hook on each is four places to forget. Inference on the save path
// would make an editor wait on the sidecar, and make saves fail when it is down
// -- an unacceptable trade for a search index. And a hook can only ever fix
// articles written after it shipped, whereas a reconciler converges the corpus
// it finds, which is the same operation as the initial backfill.
//
// So there is no backfill command: the first pass after deploy *is* the
// backfill.
type Reconciler struct {
	conn   *sql.DB
	client *Client

	// Interval between passes once the corpus has converged. A pass that does
	// real work schedules the next one immediately, so a cold start drains at
	// full speed and a warm one stays close to idle.
	Interval time.Duration

	// BatchSize is how many articles are embedded per sidecar round trip. It must
	// stay at or below the sidecar's EMBED_MAX_BATCH.
	BatchSize int
}

func NewReconciler(conn *sql.DB, client *Client) *Reconciler {
	return &Reconciler{
		conn:      conn,
		client:    client,
		Interval:  5 * time.Minute,
		BatchSize: 32,
	}
}

// Run blocks until ctx is cancelled. It is safe to run with no sidecar
// configured: it logs once and returns, leaving search lexical-only.
func (r *Reconciler) Run(ctx context.Context) {
	if !r.client.Enabled() {
		slog.Info("embedding reconciler disabled; no sidecar configured")
		return
	}

	for {
		worked, err := r.pass(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// The sidecar being unreachable is expected during its model load and
			// during deploys, so this is a warning and the loop keeps going. The
			// consequence of a long outage is stale vectors, not lost data.
			slog.Warn("embedding reconciler pass failed", "error", err)
		}

		delay := r.Interval
		if worked && err == nil {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// reconcilerLockName serializes reconcilers across processes. Delta runs two
// backend slots (blue/green), and during a deploy both are live; without this
// they would race to embed the same batch, doubling the load on a CPU-bound
// sidecar to produce identical rows.
const reconcilerLockName = "cms_embedding_reconciler"

// pass does one unit of reconciliation and reports whether it found work, so
// Run can tell a draining backlog from a converged corpus.
func (r *Reconciler) pass(ctx context.Context) (bool, error) {
	// The lock must be taken and released on one connection, so this reserves a
	// dedicated one for the pass. A zero timeout means the other slot's
	// reconciler is mid-pass and this one simply waits for the next tick.
	lockConn, err := r.conn.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer lockConn.Close()

	var acquired sql.NullInt64
	if err := lockConn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", reconcilerLockName).Scan(&acquired); err != nil {
		return false, err
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return false, nil
	}
	defer func() {
		_, _ = lockConn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", reconcilerLockName)
	}()

	model, dimensions, err := r.client.Model(ctx)
	if err != nil {
		return false, err
	}
	if dimensions != db.EmbeddingDimensions {
		// Refuse rather than write vectors the column cannot hold or, worse, that
		// silently occupy the same space as differently-shaped ones.
		return false, &DimensionMismatchError{Got: dimensions, Want: db.EmbeddingDimensions, Model: model}
	}

	if removed, err := db.DeleteOrphanedEmbeddings(ctx, r.conn); err != nil {
		return false, err
	} else if removed > 0 {
		slog.Info("removed embeddings for articles that are no longer live", "count", removed)
	}

	// Missing vectors first: an article with no embedding is invisible to
	// semantic search, which is worse than one whose embedding is a few edits
	// out of date.
	sources, err := db.ArticlesNeedingEmbeddings(ctx, r.conn, model, r.BatchSize)
	if err != nil {
		return false, err
	}
	if len(sources) == 0 {
		sources, err = db.StaleEmbeddingArticles(ctx, r.conn, r.BatchSize)
		if err != nil {
			return false, err
		}
	}
	if len(sources) == 0 {
		return false, nil
	}

	texts := make([]string, len(sources))
	for i, source := range sources {
		texts[i] = source.Text
	}

	vectors, embeddedModel, err := r.client.Embed(ctx, texts, KindDocument)
	if err != nil {
		return false, err
	}

	for i, source := range sources {
		if err := db.SaveArticleEmbedding(ctx, r.conn, source.ArticleID, vectors[i], source.Hash, embeddedModel); err != nil {
			return false, err
		}
	}

	slog.Info("embedded articles", "count", len(sources), "model", embeddedModel)
	return true, nil
}

// DimensionMismatchError means the sidecar is serving a model whose output does
// not fit the vector column -- almost always an EMBED_MODEL change that was not
// accompanied by the schema change it requires.
type DimensionMismatchError struct {
	Got   int
	Want  int
	Model string
}

func (e *DimensionMismatchError) Error() string {
	return "embeddings: sidecar model " + e.Model + " returns vectors of the wrong width for article_embeddings.embedding"
}
