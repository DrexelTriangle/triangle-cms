package embeddings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
)

// These drive the real reconciler against a real MariaDB, with the model itself
// stubbed. The model is the one part that cannot be asserted on -- what matters
// here is that the loop finds the right work, writes vectors of the right shape,
// records the hash and model that make the next pass a no-op, and converges.
//
// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/embeddings/ -run Reconciler -v
func reconcilerTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping reconciler integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	// More than one connection, unlike the other integration helpers: the
	// reconciler reserves a dedicated connection for its own advisory lock, so a
	// single-connection pool would deadlock against the test lock below.
	conn.SetMaxOpenConns(4)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	ctx := context.Background()
	// GET_LOCK is connection-scoped, so the test lock is held on a connection
	// pinned for the whole test rather than one borrowed from the pool.
	lockConn, err := conn.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve lock connection: %v", err)
	}
	var acquired sql.NullInt64
	if err := lockConn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the reconciler test lock")
	}
	t.Cleanup(func() {
		_, _ = lockConn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		lockConn.Close()
		conn.Close()
	})

	for _, table := range []string{"article_embeddings", "articles"} {
		if _, err := conn.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s table: %v", table, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			title LONGTEXT,
			slug VARCHAR(255) NOT NULL UNIQUE,
			tags LONGTEXT,
			`+"`text`"+` LONGTEXT,
			mod_date DATETIME NULL,
			pub_date DATETIME NULL,
			archived_at DATETIME NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}
	if err := db.EnsureArticleEmbeddingsTable(ctx, conn); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "vector") {
			t.Skipf("this MariaDB has no VECTOR support: %v", err)
		}
		t.Fatalf("ensure article embeddings table: %v", err)
	}

	return conn
}

// fakeSidecar returns correctly-shaped vectors and counts how many texts it was
// asked to embed, which is how these tests detect redundant work.
func fakeSidecar(t *testing.T, model string, dimensions int, embedded *atomic.Int64) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Model: model, Dimensions: dimensions})
		case "/embed":
			var request embedRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if embedded != nil {
				embedded.Add(int64(len(request.Texts)))
			}
			vectors := make([][]float32, len(request.Texts))
			for i := range vectors {
				vector := make([]float32, dimensions)
				vector[i%dimensions] = 1
				vectors[i] = vector
			}
			_ = json.NewEncoder(w).Encode(embedResponse{Model: model, Dimensions: dimensions, Vectors: vectors})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func seedArticle(t *testing.T, conn *sql.DB, slug, title, body, pubDate string) int64 {
	t.Helper()
	var pub any
	if pubDate != "" {
		pub = pubDate
	}
	result, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (title, slug, tags, `text`, pub_date, mod_date) VALUES (?, ?, '', ?, ?, ?)",
		title, slug, body, pub, pub)
	if err != nil {
		t.Fatalf("seed article %s: %v", slug, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func countEmbeddings(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var count int
	if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM article_embeddings").Scan(&count); err != nil {
		t.Fatalf("count embeddings: %v", err)
	}
	return count
}

// The whole design rests on this: a corpus with no vectors converges to a fully
// embedded one, and then stops doing work.
func TestReconciler_ConvergesThenGoesIdle(t *testing.T) {
	conn := reconcilerTestDB(t)
	var embedded atomic.Int64
	server := fakeSidecar(t, "test-model", db.EmbeddingDimensions, &embedded)

	for _, slug := range []string{"one", "two", "three", "four", "five"} {
		seedArticle(t, conn, slug, "Article "+slug, "<p>Body of "+slug+"</p>", "2026-01-01 12:00:00")
	}
	seedArticle(t, conn, "draft", "Draft", "<p>Unpublished</p>", "")

	reconciler := NewReconciler(conn, New(server.URL, 5*time.Second))
	reconciler.BatchSize = 2

	// Drain. Each pass does at most BatchSize, so this takes several.
	for i := 0; i < 10; i++ {
		worked, err := reconciler.pass(context.Background())
		if err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		if !worked {
			break
		}
	}

	if got := countEmbeddings(t, conn); got != 5 {
		t.Errorf("embedded %d articles, want 5 (the draft must be skipped)", got)
	}
	if got := embedded.Load(); got != 5 {
		t.Errorf("sidecar embedded %d texts, want 5: articles are being embedded more than once", got)
	}

	// Converged: further passes must find nothing and call nothing.
	before := embedded.Load()
	worked, err := reconciler.pass(context.Background())
	if err != nil {
		t.Fatalf("steady-state pass: %v", err)
	}
	if worked {
		t.Error("the reconciler found work in a converged corpus")
	}
	if embedded.Load() != before {
		t.Error("the reconciler re-embedded articles that were already current")
	}
}

// A body edit must be picked up; a save that changes nothing embeddable must not
// cost any inference.
func TestReconciler_ReembedsEditedArticlesOnly(t *testing.T) {
	conn := reconcilerTestDB(t)
	var embedded atomic.Int64
	server := fakeSidecar(t, "test-model", db.EmbeddingDimensions, &embedded)

	edited := seedArticle(t, conn, "edited", "Headline", "<p>Original</p>", "2026-01-01 12:00:00")
	seedArticle(t, conn, "untouched", "Other", "<p>Other body</p>", "2026-01-01 12:00:00")

	reconciler := NewReconciler(conn, New(server.URL, 5*time.Second))
	for i := 0; i < 5; i++ {
		worked, err := reconciler.pass(context.Background())
		if err != nil {
			t.Fatalf("pass: %v", err)
		}
		if !worked {
			break
		}
	}
	afterInitial := embedded.Load()

	// Edit one article's body and bump mod_date past the embedding, the way a
	// save does.
	if _, err := conn.ExecContext(context.Background(),
		"UPDATE articles SET `text` = '<p>Rewritten</p>', mod_date = UTC_TIMESTAMP() WHERE id = ?", edited); err != nil {
		t.Fatalf("edit article: %v", err)
	}

	worked, err := reconciler.pass(context.Background())
	if err != nil {
		t.Fatalf("pass after edit: %v", err)
	}
	if !worked {
		t.Fatal("the reconciler did not notice an edited article")
	}
	if got := embedded.Load() - afterInitial; got != 1 {
		t.Errorf("re-embedded %d articles after one edit, want 1", got)
	}

	// And the new hash matches the new text, so it settles rather than looping.
	var storedHash string
	if err := conn.QueryRowContext(context.Background(),
		"SELECT source_hash FROM article_embeddings WHERE article_id = ?", edited).Scan(&storedHash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if want := db.BuildEmbeddingSource(edited, "Headline", "", "<p>Rewritten</p>").Hash; storedHash != want {
		t.Errorf("stored hash %s does not match the edited text (%s); the reconciler would re-embed forever", storedHash, want)
	}
}

// An article created after the initial backfill has no vector at all, so it is
// caught by the missing-row path rather than the staleness one -- even though
// POST leaves mod_date NULL.
func TestReconciler_EmbedsArticlesCreatedAfterTheBackfill(t *testing.T) {
	conn := reconcilerTestDB(t)
	ctx := context.Background()
	server := fakeSidecar(t, "test-model", db.EmbeddingDimensions, nil)
	reconciler := NewReconciler(conn, New(server.URL, 5*time.Second))

	seedArticle(t, conn, "existing", "Existing", "<p>Body</p>", "2026-01-01 12:00:00")
	if _, err := reconciler.pass(ctx); err != nil {
		t.Fatalf("initial pass: %v", err)
	}

	// A brand-new article, the way POST writes one: no mod_date.
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO articles (title, slug, tags, `text`, pub_date, mod_date) VALUES ('Fresh', 'fresh', '', '<p>New body</p>', '2026-02-01 12:00:00', NULL)"); err != nil {
		t.Fatalf("create article: %v", err)
	}

	worked, err := reconciler.pass(ctx)
	if err != nil {
		t.Fatalf("pass after create: %v", err)
	}
	if !worked {
		t.Fatal("the reconciler did not pick up a newly created article")
	}
	if got := countEmbeddings(t, conn); got != 2 {
		t.Errorf("embedded %d articles, want 2", got)
	}
}

// Changing EMBED_MODEL without re-embedding would leave two incompatible vector
// spaces in one index, which produces confidently wrong rankings rather than an
// error. The model column is what makes that recoverable.
func TestReconciler_ReembedsEverythingAfterAModelChange(t *testing.T) {
	conn := reconcilerTestDB(t)
	server := fakeSidecar(t, "old-model", db.EmbeddingDimensions, nil)

	for _, slug := range []string{"one", "two"} {
		seedArticle(t, conn, slug, "Article "+slug, "<p>Body</p>", "2026-01-01 12:00:00")
	}

	reconciler := NewReconciler(conn, New(server.URL, 5*time.Second))
	for i := 0; i < 5; i++ {
		worked, err := reconciler.pass(context.Background())
		if err != nil {
			t.Fatalf("pass: %v", err)
		}
		if !worked {
			break
		}
	}

	var newModelEmbedded atomic.Int64
	newServer := fakeSidecar(t, "new-model", db.EmbeddingDimensions, &newModelEmbedded)
	newReconciler := NewReconciler(conn, New(newServer.URL, 5*time.Second))
	for i := 0; i < 5; i++ {
		worked, err := newReconciler.pass(context.Background())
		if err != nil {
			t.Fatalf("pass after model change: %v", err)
		}
		if !worked {
			break
		}
	}

	if got := newModelEmbedded.Load(); got != 2 {
		t.Errorf("re-embedded %d articles after a model change, want 2", got)
	}
	var stale int
	if err := conn.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM article_embeddings WHERE model <> 'new-model'").Scan(&stale); err != nil {
		t.Fatalf("count stale: %v", err)
	}
	if stale != 0 {
		t.Errorf("%d vectors still carry the old model", stale)
	}
}

// A sidecar serving a differently-shaped model must be refused outright rather
// than left to fail one INSERT at a time.
func TestReconciler_RefusesAWrongWidthModel(t *testing.T) {
	conn := reconcilerTestDB(t)
	server := fakeSidecar(t, "wrong-model", 128, nil)
	seedArticle(t, conn, "story", "Story", "<p>Body</p>", "2026-01-01 12:00:00")

	_, err := NewReconciler(conn, New(server.URL, 5*time.Second)).pass(context.Background())
	if err == nil {
		t.Fatal("the reconciler accepted a model of the wrong width")
	}
	var mismatch *DimensionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v, want a DimensionMismatchError", err)
	}
	if countEmbeddings(t, conn) != 0 {
		t.Error("wrong-width vectors were written")
	}
}

// Delta runs two backend slots and both are live during a deploy. Without the
// advisory lock they would race to embed the same batch, doubling the load on a
// CPU-bound sidecar to write identical rows.
func TestReconciler_OnlyOneInstanceWorksAtATime(t *testing.T) {
	conn := reconcilerTestDB(t)
	ctx := context.Background()
	var embedded atomic.Int64
	server := fakeSidecar(t, "test-model", db.EmbeddingDimensions, &embedded)

	seedArticle(t, conn, "story", "Story", "<p>Body</p>", "2026-01-01 12:00:00")

	// Stand in for the other slot's reconciler holding the lock mid-pass.
	otherSlot, err := conn.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve connection: %v", err)
	}
	defer otherSlot.Close()
	var acquired sql.NullInt64
	if err := otherSlot.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", reconcilerLockName).Scan(&acquired); err != nil {
		t.Fatalf("acquire reconciler lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("could not take the reconciler lock to simulate the other slot")
	}

	worked, err := NewReconciler(conn, New(server.URL, 5*time.Second)).pass(ctx)
	if err != nil {
		t.Fatalf("pass while locked out: %v", err)
	}
	if worked {
		t.Error("a second reconciler did work while another held the lock")
	}
	if embedded.Load() != 0 {
		t.Error("a locked-out reconciler still called the sidecar")
	}

	// Once the other slot finishes, this one picks the work up.
	if _, err := otherSlot.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", reconcilerLockName); err != nil {
		t.Fatalf("release reconciler lock: %v", err)
	}
	worked, err = NewReconciler(conn, New(server.URL, 5*time.Second)).pass(ctx)
	if err != nil {
		t.Fatalf("pass after release: %v", err)
	}
	if !worked || embedded.Load() != 1 {
		t.Errorf("after the lock was released the reconciler did %d work (embedded %d), want 1", boolToInt(worked), embedded.Load())
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// A missing sidecar is a supported deployment, not an error state.
func TestReconciler_StopsImmediatelyWithoutASidecar(t *testing.T) {
	conn := reconcilerTestDB(t)

	done := make(chan struct{})
	go func() {
		NewReconciler(conn, New("", time.Second)).Run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when no sidecar is configured")
	}
}

// Run has to survive an unreachable sidecar -- it is down during its own model
// load and during every deploy -- and pick up again once it returns.
func TestReconciler_SurvivesAnUnreachableSidecar(t *testing.T) {
	conn := reconcilerTestDB(t)
	seedArticle(t, conn, "story", "Story", "<p>Body</p>", "2026-01-01 12:00:00")

	// A URL that refuses connections stands in for the sidecar being down.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	reconciler := NewReconciler(conn, New(deadURL, 500*time.Millisecond))
	if _, err := reconciler.pass(context.Background()); err == nil {
		t.Fatal("a pass against a dead sidecar reported success")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reconciler.Interval = 100 * time.Millisecond

	done := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on context cancellation while the sidecar was down")
	}
}
