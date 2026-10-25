// Command import-newsletter-subscribers copies the legacy WordPress Newsletter
// plugin's subscribers (wp_newsletter) into the CMS's newsletter_subscribers.
//
// It is keyed on the WordPress id, so it can be run again until WordPress is
// retired: new WordPress subscribers are added, and anyone who unsubscribed in
// WordPress since the last run is unsubscribed here too. It never resubscribes
// anyone. Lists 1-7 keep their WordPress ids, and each subscriber's WordPress
// token is kept so the unsubscribe links in issues already sent can still be
// honoured once WordPress is gone.
//
// The source is WP_DSN (a go-sql-driver DSN for the WordPress database, read
// from the environment so the password stays out of argv). The target is the
// CMS database from DB_*, like the server. The whole import is one transaction;
// without -apply it is rolled back, so a dry run exercises exactly the writes
// a real run would make.
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
	apply := flag.Bool("apply", false, "commit the import; without it, report what would change and roll back")
	table := flag.String("table", "wp_newsletter", "the WordPress subscriber table")
	flag.Parse()

	ctx := context.Background()

	sourceDSN := strings.TrimSpace(os.Getenv("WP_DSN"))
	if sourceDSN == "" {
		log.Fatal("WP_DSN is required (user:pw@tcp(host:3306)/wordpress?parseTime=true)")
	}
	source, err := sql.Open("mysql", sourceDSN)
	if err != nil {
		log.Fatalf("open WordPress database: %v", err)
	}
	defer source.Close()

	rows, skipped, err := readLegacySubscribers(ctx, source, *table)
	if err != nil {
		log.Fatalf("read %s: %v", *table, err)
	}
	fmt.Printf("read %d importable WordPress subscribers\n", len(rows))
	for reason, ids := range skipped {
		fmt.Printf("  skipped %d (%s): wp ids %v\n", len(ids), reason, ids)
	}

	target, err := connect(ctx)
	if err != nil {
		log.Fatalf("connect to CMS database: %v", err)
	}
	defer target.Close()
	// The server creates the tables at startup. An import that ran first would
	// be importing into a schema no deployed binary knows about.
	if _, err := target.ExecContext(ctx, "SELECT 1 FROM newsletter_lists LIMIT 1"); err != nil {
		log.Fatalf("newsletter tables missing; deploy the CMS with the newsletter backend first: %v", err)
	}

	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		log.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	report, err := db.ImportLegacySubscribers(ctx, tx, rows)
	if err != nil {
		log.Fatalf("import: %v", err)
	}

	verb := "would"
	if *apply {
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit: %v", err)
		}
		verb = "did"
	}
	fmt.Printf("%s insert %d, unsubscribe %d, leave %d unchanged\n", verb, report.Inserted, report.Unsubscribed, report.Unchanged)
	if len(report.EmailTaken) > 0 {
		fmt.Printf("address already used by another CMS subscriber, not imported: wp ids %v\n", report.EmailTaken)
	}
	if !*apply {
		fmt.Println("dry run: rolled back. Pass -apply to write.")
	}
}

// wpStatuses maps the plugin's one-letter statuses. S (awaiting confirmation)
// and P are absent: the site ran single opt-in, so an unconfirmed row is an
// address that never finished subscribing.
var wpStatuses = map[string]string{
	"C": "confirmed",
	"U": "unsubscribed",
	"B": "bounced",
}

const wpListCount = 7

type wpRow struct {
	id                   int64
	email, name, surname string
	token, status        string
	created              time.Time
	updated, unsubTime   int64
	lists                [wpListCount]bool
}

func readLegacySubscribers(ctx context.Context, conn *sql.DB, table string) ([]db.LegacySubscriber, map[string][]int64, error) {
	if strings.ContainsAny(table, "`; ") {
		return nil, nil, fmt.Errorf("invalid table name %q", table)
	}
	listCols := make([]string, wpListCount)
	for i := range listCols {
		listCols[i] = "list_" + strconv.Itoa(i+1)
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT id, COALESCE(email, ''), COALESCE(name, ''), COALESCE(surname, ''), COALESCE(token, ''),
			COALESCE(status, ''), created, COALESCE(updated, 0), COALESCE(unsub_time, 0), `+strings.Join(listCols, ", ")+`
		FROM `+"`"+table+"`"+` ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var raw []wpRow
	for rows.Next() {
		var r wpRow
		dest := []any{&r.id, &r.email, &r.name, &r.surname, &r.token, &r.status, &r.created, &r.updated, &r.unsubTime}
		for i := range r.lists {
			dest = append(dest, &r.lists[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	subs, skipped := mapLegacyRows(raw)
	return subs, skipped, nil
}

// mapLegacyRows turns plugin rows into CMS subscribers, and says why it left
// any out. Rows arrive in id order, so when two rows share an address the
// older one is kept.
func mapLegacyRows(raw []wpRow) ([]db.LegacySubscriber, map[string][]int64) {
	skipped := map[string][]int64{}
	seen := map[string]bool{}
	var out []db.LegacySubscriber

	for _, r := range raw {
		email := strings.TrimSpace(r.email)
		status, known := wpStatuses[strings.ToUpper(strings.TrimSpace(r.status))]
		key := strings.ToLower(email)
		switch {
		case !known:
			skipped["status "+r.status+", never confirmed"] = append(skipped["status "+r.status+", never confirmed"], r.id)
			continue
		case !strings.Contains(email, "@") || len(email) > 255:
			skipped["invalid address"] = append(skipped["invalid address"], r.id)
			continue
		case seen[key]:
			skipped["duplicate address"] = append(skipped["duplicate address"], r.id)
			continue
		}
		seen[key] = true

		s := db.LegacySubscriber{
			WPID:         r.id,
			Email:        email,
			Name:         strings.TrimSpace(strings.TrimSpace(r.name) + " " + strings.TrimSpace(r.surname)),
			Token:        strings.TrimSpace(r.token),
			Status:       status,
			SubscribedAt: r.created,
		}
		if status != "confirmed" {
			// unsub_time is when the reader left; updated is the last change
			// to the row, the best guess when the plugin did not record it.
			for _, unix := range []int64{r.unsubTime, r.updated} {
				if unix > 0 {
					t := time.Unix(unix, 0).UTC()
					s.UnsubscribedAt = &t
					break
				}
			}
		}
		for i, on := range r.lists {
			if on {
				s.Lists = append(s.Lists, i+1)
			}
		}
		out = append(out, s)
	}
	return out, skipped
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
	// A one-off run has no business taking the server's share of the
	// connection budget, and it is single-threaded anyway.
	os.Setenv("DB_MAX_OPEN_CONNS", "2")

	timeoutCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.InitializeConnection(timeoutCtx, name, user, password, host, port)
}
