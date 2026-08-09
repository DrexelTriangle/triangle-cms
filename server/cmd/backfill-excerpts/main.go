// Command backfill-excerpts repairs articles whose `excerpt` column was blanked
// by a save.
//
// The excerpt box is optional and the editor sends it on every save, so for a
// while PUT and PATCH stored the blank verbatim while POST derived one from the
// body. An article therefore got an excerpt on create and lost it on the first
// edit afterward. Listings select `excerpt` and never the body, so a blank
// column is a story that appears on the section pages and the homepage with no
// summary under it.
//
// The write paths now derive an excerpt from the body whenever the field
// arrives blank; this repairs the rows blanked before that. It derives through
// exactly the same db.ExcerptOrDerived the server uses, so a repaired row is
// byte-identical to what a save would write today.
//
// It reads DB_* from the environment, like the server, so it can be run inside
// the backend service's own environment rather than with credentials copied by
// hand. Dry run by default: pass -apply to write.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	db "server/internal/database"
)

func main() {
	apply := flag.Bool("apply", false, "write the derived excerpts; without it, report what would change")
	limit := flag.Int("limit", 0, "stop after this many articles (0 = all)")
	verbose := flag.Bool("verbose", false, "list every article, not just the first few of each kind")
	flag.Parse()

	ctx := context.Background()
	conn, err := connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	// Read every blank row up front rather than streaming: the UPDATEs below run
	// against the same table the cursor is walking, and the whole set is a few
	// hundred rows.
	type article struct {
		id      int64
		slug    string
		content string
	}
	query := "SELECT `id`, `slug`, COALESCE(`text`, '') FROM `articles` WHERE TRIM(COALESCE(`excerpt`, '')) = '' ORDER BY `id`"
	if *limit > 0 {
		query += " LIMIT " + strconv.Itoa(*limit)
	}
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		log.Fatalf("select blank excerpts: %v", err)
	}
	var blank []article
	for rows.Next() {
		var a article
		if err := rows.Scan(&a.id, &a.slug, &a.content); err != nil {
			rows.Close()
			log.Fatalf("scan: %v", err)
		}
		blank = append(blank, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.Fatalf("iterate: %v", err)
	}
	rows.Close()

	fmt.Printf("%d articles have a blank excerpt\n", len(blank))

	var repaired, empty int
	for _, a := range blank {
		derived := db.ExcerptOrDerived("", a.content)
		if strings.TrimSpace(derived) == "" {
			// Nothing to derive from -- a body that is a bare shortcode, an
			// image-only post, or a genuinely empty draft. Leave it alone: an
			// empty excerpt is what it had, and there is no text to improve on.
			if *verbose || empty < 10 {
				fmt.Printf("  [skip] %s (body %d bytes: %s)\n", a.slug, len(a.content), truncate(strings.Join(strings.Fields(a.content), " "), 70))
			}
			empty++
			continue
		}
		if !*apply {
			if *verbose || repaired < 10 {
				fmt.Printf("  %s -> %s\n", a.slug, truncate(derived, 80))
			}
			repaired++
			continue
		}
		// mod_date is deliberately left alone. This is a repair of a value the
		// CMS should have written at the time, not an edit: touching mod_date
		// would move every repaired article's sitemap lastmod to today and queue
		// the whole corpus for re-embedding.
		if _, err := conn.ExecContext(ctx,
			"UPDATE `articles` SET `excerpt` = ? WHERE `id` = ? AND TRIM(COALESCE(`excerpt`, '')) = ''",
			derived, a.id,
		); err != nil {
			log.Fatalf("update %s: %v", a.slug, err)
		}
		repaired++
	}

	verb := "would repair"
	if *apply {
		verb = "repaired"
	}
	fmt.Printf("%s %d articles; %d left alone (no text to derive from)\n", verb, repaired, empty)
}

func connect(ctx context.Context) (*sql.DB, error) {
	name := strings.TrimSpace(os.Getenv("DB_NAME"))
	user := strings.TrimSpace(os.Getenv("DB_USER"))
	password := os.Getenv("DB_PASSWORD")
	host := strings.TrimSpace(os.Getenv("DB_HOST"))
	if name == "" || user == "" {
		return nil, fmt.Errorf("DB_NAME and DB_USER are required")
	}
	if host == "" {
		host = "127.0.0.1"
	}
	port := 3306
	if raw := strings.TrimSpace(os.Getenv("DB_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("DB_PORT %q: %w", raw, err)
		}
		port = parsed
	}
	// A one-off maintenance run has no business taking the server's share of a
	// 200-connection budget, and it is single-threaded anyway.
	os.Setenv("DB_MAX_OPEN_CONNS", "2")

	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.InitializeConnection(timeoutCtx, name, user, password, host, port)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
