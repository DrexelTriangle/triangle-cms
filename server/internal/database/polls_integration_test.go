package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// These tests need a real MariaDB: the poll code leans on server-side behaviour
// (ON DELETE CASCADE, multi-table UPDATE ... JOIN, unique constraints) that a
// mock would not reproduce, and the legacy migration is exactly the kind of
// thing that fails silently. They skip unless CMS_TEST_DSN is set, so CI without
// a database stays green.
//
//	CMS_TEST_DSN='root:pw@tcp(127.0.0.1:3306)/poll_test?parseTime=true&multiStatements=true' go test ./internal/database/ -run Poll -v
func pollTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("CMS_TEST_DSN")
	if dsn == "" {
		t.Skip("CMS_TEST_DSN not set; skipping poll database integration test")
	}

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping test database: %v", err)
	}

	ctx := context.Background()
	// Drop children before parents; the FK on cms_poll_options blocks the reverse.
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS " + PollOptionsTableName,
		"DROP TABLE IF EXISTS " + PollsTableName,
		"DROP TABLE IF EXISTS " + LegacyPollTableName,
		"DROP TABLE IF EXISTS cms_settings",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset schema (%s): %v", stmt, err)
		}
	}

	if err := EnsureSettingsTable(ctx, conn); err != nil {
		t.Fatalf("ensure settings table: %v", err)
	}
	if err := EnsurePollsTable(ctx, conn); err != nil {
		t.Fatalf("ensure legacy poll table: %v", err)
	}
	if err := EnsurePollsSchema(ctx, conn); err != nil {
		t.Fatalf("ensure poll schema: %v", err)
	}

	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestPollMigrateLegacyPoll(t *testing.T) {
	conn := pollTestDB(t)
	ctx := context.Background()

	legacy := map[string]int64{"Spotify": 54, "Apple Music": 26, "Pandora": 2}
	for name, votes := range legacy {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO "+LegacyPollTableName+" (option_name, vote_count) VALUES (?, ?)", name, votes); err != nil {
			t.Fatalf("seed legacy option: %v", err)
		}
	}
	if err := SetPollTitle(ctx, conn, "Where do you stream music?"); err != nil {
		t.Fatalf("seed legacy title: %v", err)
	}

	if err := MigrateLegacyPoll(ctx, conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	poll, err := GetActivePoll(ctx, conn)
	if err != nil {
		t.Fatalf("get active poll after migration: %v", err)
	}
	if poll.Question != "Where do you stream music?" {
		t.Fatalf("question = %q, want the legacy title", poll.Question)
	}
	if len(poll.Options) != len(legacy) {
		t.Fatalf("options = %d, want %d", len(poll.Options), len(legacy))
	}
	// Vote counts must survive the move; losing them silently discards real data.
	for _, opt := range poll.Options {
		if want := legacy[opt.Name]; opt.VoteCount != want {
			t.Errorf("option %q votes = %d, want %d", opt.Name, opt.VoteCount, want)
		}
	}
	if total := poll.TotalVotes(); total != 82 {
		t.Errorf("total votes = %d, want 82", total)
	}

	// Second run must not duplicate: startup calls this on every boot.
	if err := MigrateLegacyPoll(ctx, conn); err != nil {
		t.Fatalf("migrate second run: %v", err)
	}
	var count int64
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+PollsTableName).Scan(&count); err != nil {
		t.Fatalf("count polls: %v", err)
	}
	if count != 1 {
		t.Fatalf("polls after re-running migration = %d, want 1", count)
	}
}

func TestPollOnlyOneActiveAtATime(t *testing.T) {
	conn := pollTestDB(t)
	ctx := context.Background()

	first, err := CreatePoll(ctx, conn, "First", PollStatusActive, nil, nil, []string{"a", "b"})
	if err != nil {
		t.Fatalf("create first poll: %v", err)
	}
	second, err := CreatePoll(ctx, conn, "Second", PollStatusActive, nil, nil, []string{"c", "d"})
	if err != nil {
		t.Fatalf("create second poll: %v", err)
	}

	active, err := GetActivePoll(ctx, conn)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.ID != second {
		t.Fatalf("active poll = %d, want the newly created %d", active.ID, second)
	}

	// The displaced poll must be closed, not deleted: its results are the archive.
	older, err := GetPollByID(ctx, conn, first)
	if err != nil {
		t.Fatalf("get first poll: %v", err)
	}
	if older.Status != PollStatusClosed {
		t.Fatalf("displaced poll status = %q, want %q", older.Status, PollStatusClosed)
	}

	// Promoting the older one back must likewise close the newer one.
	status := PollStatusActive
	if err := UpdatePoll(ctx, conn, first, nil, &status, nil, nil, false, false); err != nil {
		t.Fatalf("reactivate first poll: %v", err)
	}
	var activeCount int64
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+PollsTableName+" WHERE status = ?", PollStatusActive).Scan(&activeCount); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if activeCount != 1 {
		t.Fatalf("active polls = %d, want 1", activeCount)
	}
}

func TestPollVoting(t *testing.T) {
	conn := pollTestDB(t)
	ctx := context.Background()

	id, err := CreatePoll(ctx, conn, "Favourite food truck?", PollStatusActive, nil, nil, []string{"Pete's", "Other"})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}

	poll, err := VoteOnActivePoll(ctx, conn, "Pete's")
	if err != nil {
		t.Fatalf("vote: %v", err)
	}
	if poll.TotalVotes() != 1 {
		t.Fatalf("total votes = %d, want 1", poll.TotalVotes())
	}

	// An option that exists on no poll must not be silently accepted.
	if _, err := VoteOnActivePoll(ctx, conn, "Nonexistent"); err != sql.ErrNoRows {
		t.Fatalf("vote for unknown option err = %v, want sql.ErrNoRows", err)
	}

	// A closed poll must reject votes even though it still has options.
	closed := PollStatusClosed
	if err := UpdatePoll(ctx, conn, id, nil, &closed, nil, nil, false, false); err != nil {
		t.Fatalf("close poll: %v", err)
	}
	if _, err := VoteOnActivePoll(ctx, conn, "Pete's"); err != ErrNoActivePoll {
		t.Fatalf("vote on closed poll err = %v, want ErrNoActivePoll", err)
	}
}

func TestPollExpiredWindowRejectsVotes(t *testing.T) {
	conn := pollTestDB(t)
	ctx := context.Background()

	// Active status but an end date in the past: the window check is what must
	// stop the vote, since nothing sweeps expired polls into "closed".
	start := time.Now().Add(-48 * time.Hour)
	end := time.Now().Add(-24 * time.Hour)
	if _, err := CreatePoll(ctx, conn, "Expired", PollStatusActive, &start, &end, []string{"yes", "no"}); err != nil {
		t.Fatalf("create expired poll: %v", err)
	}

	if _, err := VoteOnActivePoll(ctx, conn, "yes"); err != ErrPollNotOpen {
		t.Fatalf("vote on expired poll err = %v, want ErrPollNotOpen", err)
	}
}

func TestPollListExcludesDrafts(t *testing.T) {
	conn := pollTestDB(t)
	ctx := context.Background()

	if _, err := CreatePoll(ctx, conn, "Published", PollStatusClosed, nil, nil, []string{"a"}); err != nil {
		t.Fatalf("create published poll: %v", err)
	}
	if _, err := CreatePoll(ctx, conn, "Unpublished", PollStatusDraft, nil, nil, []string{"b"}); err != nil {
		t.Fatalf("create draft poll: %v", err)
	}

	public, err := ListPolls(ctx, conn, false, 50, 0)
	if err != nil {
		t.Fatalf("list public polls: %v", err)
	}
	if len(public) != 1 || public[0].Question != "Published" {
		t.Fatalf("public archive = %+v, want only the published poll", public)
	}

	all, err := ListPolls(ctx, conn, true, 50, 0)
	if err != nil {
		t.Fatalf("list all polls: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("editor listing = %d polls, want 2", len(all))
	}
}

func TestPollDeleteCascadesOptions(t *testing.T) {
	conn := pollTestDB(t)
	ctx := context.Background()

	id, err := CreatePoll(ctx, conn, "Doomed", PollStatusClosed, nil, nil, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}
	if err := DeletePoll(ctx, conn, id); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	var orphans int64
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+PollOptionsTableName+" WHERE poll_id = ?", id).Scan(&orphans); err != nil {
		t.Fatalf("count orphaned options: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("orphaned options = %d, want 0", orphans)
	}
}
