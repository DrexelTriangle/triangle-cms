package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

const (
	defaultListLimit = 100
	maxListLimit     = 500
	activityTable    = "cms_activity"
)

// SQLStore persists activity/log entries in a shared MariaDB table. Unlike the
// former embedded Badger store it holds no on-disk state and no directory lock,
// so multiple CMS processes (e.g. blue/green deploy slots) can write to and read
// from the same history concurrently.
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore ensures the activity table exists and returns a store backed by
// the provided connection. It does not take ownership of db; Close is a no-op.
func NewSQLStore(ctx context.Context, db *sql.DB) (*SQLStore, error) {
	if db == nil {
		return nil, fmt.Errorf("activity: nil database connection")
	}
	if err := ensureActivityTable(ctx, db); err != nil {
		return nil, err
	}
	return &SQLStore{db: db}, nil
}

func ensureActivityTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS `+activityTable+` (
			id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			ts          DATETIME(6) NOT NULL,
			level       VARCHAR(16) NOT NULL DEFAULT '',
			message     TEXT NULL,
			kind        VARCHAR(32) NOT NULL DEFAULT '',
			action      VARCHAR(255) NOT NULL DEFAULT '',
			actor_id    BIGINT NOT NULL DEFAULT 0,
			actor_name  VARCHAR(255) NOT NULL DEFAULT '',
			actor_role  VARCHAR(64) NOT NULL DEFAULT '',
			target      TEXT NULL,
			method      VARCHAR(16) NOT NULL DEFAULT '',
			path        VARCHAR(512) NOT NULL DEFAULT '',
			status      INT NOT NULL DEFAULT 0,
			attributes  JSON NULL,
			KEY idx_cms_activity_kind_id (kind, id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`)
	return err
}

// Close is a no-op: the store borrows a *sql.DB owned by the caller.
func (s *SQLStore) Close() error { return nil }

func (s *SQLStore) Write(ctx context.Context, entry Entry) error {
	if s == nil || s.db == nil {
		return ErrStoreUnavailable
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	} else {
		entry.Timestamp = entry.Timestamp.UTC()
	}
	if entry.Kind == "" {
		if entry.Action != "" {
			entry.Kind = "activity"
		} else {
			entry.Kind = "log"
		}
	}

	var attributes any
	if len(entry.Attributes) > 0 {
		payload, err := json.Marshal(entry.Attributes)
		if err != nil {
			return fmt.Errorf("marshal activity attributes: %w", err)
		}
		attributes = string(payload)
	}

	// Note: do not log from this path — the default logger tees into this store,
	// so logging here would recurse.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO `+activityTable+`
			(ts, level, message, kind, action, actor_id, actor_name, actor_role, target, method, path, status, attributes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		entry.Timestamp, entry.Level, nullableString(entry.Message), entry.Kind, entry.Action,
		entry.ActorID, entry.ActorName, entry.ActorRole, nullableString(entry.Target),
		entry.Method, entry.Path, entry.Status, attributes,
	)
	if err != nil {
		return fmt.Errorf("insert activity entry: %w", err)
	}
	return nil
}

func (s *SQLStore) List(ctx context.Context, query Query) (ListResult, error) {
	if s == nil || s.db == nil {
		return ListResult{}, ErrStoreUnavailable
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	where := ""
	var args []any
	if query.Kind != "" {
		where = " WHERE kind = ?"
		args = append(args, query.Kind)
	}

	result := ListResult{Entries: make([]Entry, 0, limit)}

	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+activityTable+where, args...,
	).Scan(&result.TotalCount); err != nil {
		return ListResult{}, fmt.Errorf("count activity entries: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT id, ts, level, message, kind, action, actor_id, actor_name, actor_role, target, method, path, status, attributes "+
			"FROM "+activityTable+where+" ORDER BY id DESC LIMIT ?",
		append(args, limit)...,
	)
	if err != nil {
		return ListResult{}, fmt.Errorf("query activity entries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		entry, err := scanActivityRow(rows)
		if err != nil {
			return ListResult{}, err
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate activity entries: %w", err)
	}

	return result, nil
}

func scanActivityRow(rows *sql.Rows) (Entry, error) {
	var (
		entry      Entry
		id         int64
		message    sql.NullString
		target     sql.NullString
		attributes sql.NullString
	)
	if err := rows.Scan(
		&id, &entry.Timestamp, &entry.Level, &message, &entry.Kind, &entry.Action,
		&entry.ActorID, &entry.ActorName, &entry.ActorRole, &target,
		&entry.Method, &entry.Path, &entry.Status, &attributes,
	); err != nil {
		return Entry{}, fmt.Errorf("scan activity entry: %w", err)
	}

	entry.ID = strconv.FormatInt(id, 10)
	entry.Message = message.String
	entry.Target = target.String
	if attributes.Valid && attributes.String != "" {
		if err := json.Unmarshal([]byte(attributes.String), &entry.Attributes); err != nil {
			return Entry{}, fmt.Errorf("decode activity attributes: %w", err)
		}
	}
	return entry, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
