package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// clientFoundRows makes an UPDATE report the rows it matched rather than the
// rows whose values it changed.
//
// Every handler that reads RowsAffected treats zero as "no such row" and
// answers 404: author update, patch, archive and restore, and both article
// write paths. Without this flag a save that sets every column to what it
// already holds matches its row, changes nothing, reports zero, and is answered
// as though the article had been deleted mid-edit. The editor sends the whole
// form on every save and autosaves on a timer, so two saves inside the same
// second (a double-clicked Save, or an autosave landing on an untouched form
// after mod_date has already been stamped to the current second) are enough.
//
// No call site uses RowsAffected to ask whether anything changed, so matched
// rows is what all six of them already mean.
func buildDSN(dbName, user, password, host string, port int) string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&clientFoundRows=true", user, password, host, port, dbName)
}

// defaultMaxOpenConns is the size of the connection pool.
//
// This was 10, which is a development-sized pool: it is not the database that
// runs out, it is the queue in front of it. Under production traffic every page
// render calls /v1/settings/site, and with ten connections those requests
// serialize, observed at 25-125s per request on the public cutover, while the
// database itself sat at two running threads and the host at 0.02 load. The
// symptom reads like a resource shortage everywhere except where the resource
// actually is.
//
// DB1 allows 200 connections. Two blue/green slots at 50 each leaves ample room
// for MaxScale's monitor, the replica, and an operator session.
const defaultMaxOpenConns = 50

// maxOpenConns allows the pool to be resized without a rebuild, which matters
// because the failure mode above is only visible under real traffic.
func maxOpenConns() int {
	raw := strings.TrimSpace(os.Getenv("DB_MAX_OPEN_CONNS"))
	if raw == "" {
		return defaultMaxOpenConns
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		slog.Warn("ignoring invalid DB_MAX_OPEN_CONNS, using the default",
			"value", raw, "default", defaultMaxOpenConns)
		return defaultMaxOpenConns
	}
	return parsed
}

func InitializeConnection(ctx context.Context, dbName, user, password, host string, port int) (*sql.DB, error) {
	dataSourceName := buildDSN(dbName, user, password, host, port)
	db, err := sql.Open("mysql", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open mysql connection: %w", err)
	}

	db.SetConnMaxLifetime(time.Minute * 3)
	maxConns := maxOpenConns()
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return db, nil
}
