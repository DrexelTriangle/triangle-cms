package database

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"server/internal/models"

	_ "github.com/go-sql-driver/mysql"
)

// The banner is resolved entirely in SQL -- the published predicate, the
// window, and "newest wins" are all in one query -- so these need a real
// MariaDB. The interesting case is the scheduled one: an article whose
// pub_date is in the future must not raise the banner early, and UTC_TIMESTAMP
// comparisons against DATETIME columns are exactly what a fake would get wrong.
//
// CMS_TEST_DSN='user:pw@tcp(127.0.0.1:3306)/cms_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run BreakingNewsState -v
func breakingNewsTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping breaking-news integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	ctx := context.Background()
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", "cms_integration_test_shared_tables").Scan(&acquired); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		t.Fatal("timed out waiting for the shared table lock")
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "cms_integration_test_shared_tables")
		conn.Close()
	})

	for _, stmt := range []string{
		"DROP TABLE IF EXISTS articles",
		"DROP TABLE IF EXISTS cms_settings",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset schema (%s): %v", stmt, err)
		}
	}

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE articles (
			id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			title LONGTEXT,
			slug VARCHAR(255) NOT NULL UNIQUE,
			breaking_news BOOL,
			pub_date DATETIME NULL,
			scheduled_pub_date DATETIME NULL,
			archived_at DATETIME NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`); err != nil {
		t.Fatalf("create articles table: %v", err)
	}
	if err := EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	if err := EnsureArticlesBreakingNewsIndex(ctx, conn); err != nil {
		t.Fatalf("ensure breaking-news index: %v", err)
	}

	// The settings cache is per-process and outlives the table drop above, so
	// a value written by an earlier test would be served here as if it were
	// still stored. Clearing it makes each case start from real state.
	ResetSettingsCache()

	return conn
}

// insertArticle adds one article. pubExpr and schedExpr are SQL expressions so
// a case can say "an hour ago" or "in an hour" relative to the server clock
// rather than the test's, which is the comparison the query actually makes.
func insertArticle(t *testing.T, conn *sql.DB, slug, title string, breaking bool, pubExpr, schedExpr string) {
	t.Helper()
	flag := 0
	if breaking {
		flag = 1
	}
	_, err := conn.ExecContext(context.Background(),
		"INSERT INTO articles (slug, title, breaking_news, pub_date, scheduled_pub_date) VALUES (?, ?, ?, "+pubExpr+", "+schedExpr+")",
		slug, title, flag)
	if err != nil {
		t.Fatalf("insert article %s: %v", slug, err)
	}
}

func TestBreakingNewsState_FallsBackToTheManualBanner(t *testing.T) {
	conn := breakingNewsTestDB(t)
	ctx := context.Background()

	if err := SetBreakingNews(ctx, conn, models.BreakingNewsSettings{Enabled: true, Text: "Campus closed"}, breakingNewsWindowUnlimited); err != nil {
		t.Fatalf("set manual banner: %v", err)
	}

	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !state.Enabled || state.Text != "Campus closed" {
		t.Errorf("expected the manual banner, got %+v", state.BreakingNewsSettings)
	}
	if state.Source != models.BreakingNewsSourceManual {
		t.Errorf("source = %q, want %q", state.Source, models.BreakingNewsSourceManual)
	}
	if state.WindowHours != breakingNewsWindowUnlimited {
		t.Errorf("window_hours = %d, want no limit", state.WindowHours)
	}
}

func TestBreakingNewsState_PublishedArticleOverridesTheManualBanner(t *testing.T) {
	conn := breakingNewsTestDB(t)
	ctx := context.Background()

	if err := SetBreakingNews(ctx, conn, models.BreakingNewsSettings{Enabled: true, Text: "Campus closed"}, breakingNewsWindowUnlimited); err != nil {
		t.Fatalf("set manual banner: %v", err)
	}
	insertArticle(t, conn, "dragonfly", "Dragonfly headliner", true, "UTC_TIMESTAMP() - INTERVAL 10 MINUTE", "NULL")

	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !state.Enabled || state.Text != "Dragonfly headliner" {
		t.Errorf("expected the article to drive the banner, got %+v", state.BreakingNewsSettings)
	}
	if state.Source != models.BreakingNewsSourceArticle {
		t.Errorf("source = %q, want %q", state.Source, models.BreakingNewsSourceArticle)
	}
	// The slug rides on the banner itself, not just the settings view: it is
	// what the public site builds the banner's link from.
	if state.BreakingNewsSettings.ArticleSlug != "dragonfly" {
		t.Errorf("article_slug = %q, want %q", state.BreakingNewsSettings.ArticleSlug, "dragonfly")
	}
	// The manual banner is preserved so the settings screen can still edit it.
	if !state.Manual.Enabled || state.Manual.Text != "Campus closed" {
		t.Errorf("manual banner was not preserved, got %+v", state.Manual)
	}
	// A hand-typed banner has no article behind it, so it must not inherit the
	// overriding article's link.
	if state.Manual.ArticleSlug != "" {
		t.Errorf("manual banner carried a slug: %q", state.Manual.ArticleSlug)
	}
}

func TestBreakingNewsState_ScheduledArticleWaitsForItsPubDate(t *testing.T) {
	conn := breakingNewsTestDB(t)
	ctx := context.Background()

	// Exactly what an editor scheduling an 11am story leaves behind: flagged,
	// not yet published, waiting on the scheduler tick.
	insertArticle(t, conn, "tomorrow", "Scheduled scoop", true, "NULL", "UTC_TIMESTAMP() + INTERVAL 1 HOUR")

	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Enabled {
		t.Fatalf("a scheduled article raised the banner early: %+v", state)
	}

	// Publish it the way the scheduler does, then it takes the banner with no
	// further action -- that is the whole point of deriving it.
	if _, err := conn.ExecContext(ctx,
		"UPDATE articles SET pub_date = UTC_TIMESTAMP(), scheduled_pub_date = NULL WHERE slug = ?", "tomorrow"); err != nil {
		t.Fatalf("publish article: %v", err)
	}

	state, err = GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state after publish: %v", err)
	}
	if !state.Enabled || state.Text != "Scheduled scoop" {
		t.Errorf("expected the published article to take the banner, got %+v", state.BreakingNewsSettings)
	}
	if state.BreakingNewsSettings.ArticleSlug != "tomorrow" {
		t.Errorf("article_slug = %q, want %q", state.BreakingNewsSettings.ArticleSlug, "tomorrow")
	}
}

func TestBreakingNewsState_NewestFlaggedArticleWins(t *testing.T) {
	conn := breakingNewsTestDB(t)
	ctx := context.Background()

	insertArticle(t, conn, "older", "Older breaking story", true, "UTC_TIMESTAMP() - INTERVAL 3 HOUR", "NULL")
	insertArticle(t, conn, "newer", "Newer breaking story", true, "UTC_TIMESTAMP() - INTERVAL 1 HOUR", "NULL")

	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Text != "Newer breaking story" {
		t.Errorf("text = %q, want the newer story", state.Text)
	}
}

// The default. An article flagged months ago and never unticked still holds the
// banner, because nothing was configured to take it down.
func TestBreakingNewsState_KeepsAnOldArticleWhenNoWindowIsSet(t *testing.T) {
	conn := breakingNewsTestDB(t)
	ctx := context.Background()

	insertArticle(t, conn, "ancient", "Long-forgotten emergency", true, "UTC_TIMESTAMP() - INTERVAL 30 DAY", "NULL")

	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !state.Enabled || state.Text != "Long-forgotten emergency" {
		t.Errorf("expected the article to still hold the banner, got %+v", state.BreakingNewsSettings)
	}
	if state.WindowHours != breakingNewsWindowUnlimited {
		t.Errorf("window_hours = %d, want no limit by default", state.WindowHours)
	}
}

func TestBreakingNewsState_IgnoresArticlesOutsideAnAdminSetWindow(t *testing.T) {
	conn := breakingNewsTestDB(t)
	ctx := context.Background()

	if err := SetBreakingNews(ctx, conn, models.BreakingNewsSettings{}, 2); err != nil {
		t.Fatalf("set window: %v", err)
	}
	insertArticle(t, conn, "stale", "Yesterday's emergency", true, "UTC_TIMESTAMP() - INTERVAL 5 HOUR", "NULL")

	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Enabled {
		t.Errorf("an article past the window still held the banner: %+v", state)
	}
	if state.Source != models.BreakingNewsSourceNone {
		t.Errorf("source = %q, want %q", state.Source, models.BreakingNewsSourceNone)
	}
}

func TestBreakingNewsState_IgnoresArchivedAndUnflaggedArticles(t *testing.T) {
	conn := breakingNewsTestDB(t)
	ctx := context.Background()

	insertArticle(t, conn, "plain", "Not breaking", false, "UTC_TIMESTAMP() - INTERVAL 5 MINUTE", "NULL")
	insertArticle(t, conn, "pulled", "Pulled story", true, "UTC_TIMESTAMP() - INTERVAL 5 MINUTE", "NULL")
	if _, err := conn.ExecContext(ctx, "UPDATE articles SET archived_at = UTC_TIMESTAMP() WHERE slug = ?", "pulled"); err != nil {
		t.Fatalf("archive article: %v", err)
	}

	state, err := GetBreakingNewsState(ctx, conn)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if state.Enabled {
		t.Errorf("expected no banner, got %+v", state)
	}
}
