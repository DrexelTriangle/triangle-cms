package database

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"server/internal/models"
)

func EnsureArticlesSchema(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		ALTER TABLE articles
		ADD COLUMN IF NOT EXISTS archived_at DATETIME NULL DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS breaking_news BOOL NULL DEFAULT 0,
		ADD COLUMN IF NOT EXISTS focus_keyword LONGTEXT NULL DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS meta_description LONGTEXT NULL DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS seo_title LONGTEXT NULL DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS scheduled_pub_date DATETIME NULL DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS last_pub_date DATETIME NULL DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS canonical_url LONGTEXT NULL DEFAULT NULL,
		ADD COLUMN IF NOT EXISTS noindex BOOL NOT NULL DEFAULT 0,
		ADD COLUMN IF NOT EXISTS photo_alt LONGTEXT NULL DEFAULT NULL
	`)
	return err
}

// EnsureArticlesSearchIndex adds the FULLTEXT indexes public search ranks on.
//
// Two indexes, not one: the combined index answers "does this article match at
// all", and the title-only index supplies the boost that keeps a headline hit
// above a body mention. MATCH can only use an index whose column list it names
// exactly, so the pair is the minimum that expresses that ranking.
//
// Callers must treat a failure here as non-fatal. Building the first FULLTEXT
// index on `articles` forces InnoDB to rebuild the table (it adds a hidden
// FTS_DOC_ID column), which on the migrated WordPress corpus takes long enough
// to look like a stalled boot. SearchArticles falls back to LIKE whenever the
// indexes are absent, so a slow or failed ALTER degrades search instead of
// taking the CMS down with it.
func EnsureArticlesSearchIndex(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		ALTER TABLE articles
		ADD FULLTEXT INDEX IF NOT EXISTS ft_articles_search (`+"`title`, `tags`, `text`"+`),
		ADD FULLTEXT INDEX IF NOT EXISTS ft_articles_title (`+"`title`"+`)
	`)
	return err
}

// BackfillArticleSEOFromYoast copies focus keyphrase / meta description / SEO
// title from the WordPress `seo` export (yoast_tag_data JSON) into the article
// columns. It only fills blanks, so editor changes are never overwritten, and it
// records a one-time flag in cms_settings so it doesn't run again. It's a no-op
// when the export table isn't present (e.g. production without the seed import).
func BackfillArticleSEOFromYoast(ctx context.Context, conn *sql.DB) error {
	var seoTableExists int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'seo'",
	).Scan(&seoTableExists); err != nil {
		return err
	}
	if seoTableExists == 0 {
		return nil
	}

	var done string
	switch err := conn.QueryRowContext(ctx,
		"SELECT value_text FROM cms_settings WHERE key_name = 'articles_seo_backfilled' LIMIT 1",
	).Scan(&done); err {
	case nil:
		if strings.TrimSpace(done) == "1" {
			return nil
		}
	case sql.ErrNoRows:
		// Not yet backfilled.
	default:
		return err
	}

	if _, err := conn.ExecContext(ctx, `
		UPDATE articles a
		JOIN seo s ON s.article_id = a.id
		SET
			a.focus_keyword    = COALESCE(NULLIF(a.focus_keyword, ''),    NULLIF(JSON_VALUE(s.yoast_tag_data, '$._yoast_wpseo_focuskw'), '')),
			a.meta_description = COALESCE(NULLIF(a.meta_description, ''), NULLIF(JSON_VALUE(s.yoast_tag_data, '$._yoast_wpseo_metadesc'), '')),
			a.seo_title        = COALESCE(NULLIF(a.seo_title, ''),        NULLIF(JSON_VALUE(s.yoast_tag_data, '$._yoast_wpseo_title'), ''))
	`); err != nil {
		return err
	}

	// Through the cached writer so the flag cannot be read back stale; this runs
	// once at startup, but leaving one inline INSERT behind is how the cache
	// quietly diverges later.
	return writeSettingRaw(ctx, conn, "articles_seo_backfilled", "1")
}

func EnsureAuthorsSchema(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		ALTER TABLE authors
		ADD COLUMN IF NOT EXISTS archived_at DATETIME NULL DEFAULT NULL
	`)
	return err
}

func EnsureUsersTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cms_users (
			id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			sub          VARCHAR(255) NOT NULL,
			email        VARCHAR(255) NOT NULL,
			name         VARCHAR(255) NOT NULL DEFAULT '',
			role         ENUM('editor','admin') NOT NULL DEFAULT 'editor',
			author_id    BIGINT UNSIGNED NULL,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_login_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE KEY uq_cms_users_sub (sub),
			UNIQUE KEY uq_cms_users_email (email)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`)
	return err
}

func FindOrCreateUser(ctx context.Context, conn *sql.DB, sub, email, name string) (*models.User, error) {
	user, err := findUserBySub(ctx, conn, sub)
	if err == nil {
		_ = updateLastLogin(ctx, conn, user.ID)
		return user, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	authorID := findAuthorIDByEmail(ctx, conn, email)

	id, role, err := insertUser(ctx, conn, sub, email, name, authorID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	return &models.User{
		ID:          id,
		Sub:         sub,
		Email:       email,
		Name:        name,
		Role:        role,
		AuthorID:    authorID,
		CreatedAt:   now,
		LastLoginAt: now,
	}, nil
}

// insertUser creates a CMS user, bootstrapping the very first one as an admin so
// a fresh install has someone who can manage roles. Everyone after that starts as
// an editor and has to be promoted from the users screen. The count and the
// insert share a transaction (and a locking read) so two simultaneous first
// logins can't both come out as admin.
func insertUser(ctx context.Context, conn *sql.DB, sub, email, name string, authorID *int64) (int64, models.Role, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = tx.Rollback() }()

	var existing int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM cms_users FOR UPDATE").Scan(&existing); err != nil {
		return 0, "", err
	}

	role := models.RoleEditor
	if existing == 0 {
		role = models.RoleAdmin
	}

	res, err := tx.ExecContext(ctx,
		"INSERT INTO cms_users (sub, email, name, role, author_id) VALUES (?, ?, ?, ?, ?)",
		sub, email, name, role, authorID,
	)
	if err != nil {
		return 0, "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	return id, role, nil
}

func findUserBySub(ctx context.Context, conn *sql.DB, sub string) (*models.User, error) {
	row := conn.QueryRowContext(ctx,
		"SELECT id, sub, email, name, role, author_id, created_at, last_login_at FROM cms_users WHERE sub = ?",
		sub,
	)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*models.User, error) {
	var u models.User
	var authorID sql.NullInt64
	err := row.Scan(&u.ID, &u.Sub, &u.Email, &u.Name, &u.Role, &authorID, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, err
	}
	if authorID.Valid {
		u.AuthorID = &authorID.Int64
	}
	return &u, nil
}

func updateLastLogin(ctx context.Context, conn *sql.DB, id int64) error {
	_, err := conn.ExecContext(ctx, "UPDATE cms_users SET last_login_at = NOW() WHERE id = ?", id)
	return err
}

func findAuthorIDByEmail(ctx context.Context, conn *sql.DB, email string) *int64 {
	var id int64
	if err := conn.QueryRowContext(ctx, "SELECT id FROM authors WHERE email = ? LIMIT 1", email).Scan(&id); err != nil {
		return nil
	}
	return &id
}

func ListUsers(ctx context.Context, conn *sql.DB) ([]models.User, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT id, sub, email, name, role, author_id, created_at, last_login_at FROM cms_users ORDER BY created_at DESC",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		var u models.User
		var authorID sql.NullInt64
		if err := rows.Scan(&u.ID, &u.Sub, &u.Email, &u.Name, &u.Role, &authorID, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		if authorID.Valid {
			u.AuthorID = &authorID.Int64
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func GetUserByID(ctx context.Context, conn *sql.DB, id int64) (*models.User, error) {
	row := conn.QueryRowContext(ctx,
		"SELECT id, sub, email, name, role, author_id, created_at, last_login_at FROM cms_users WHERE id = ?",
		id,
	)
	return scanUser(row)
}

func UpdateUserRole(ctx context.Context, conn *sql.DB, id int64, role models.Role) error {
	_, err := conn.ExecContext(ctx, "UPDATE cms_users SET role = ? WHERE id = ?", role, id)
	return err
}

func ArticleHasAuthor(ctx context.Context, conn *sql.DB, articleSlug string, authorID int64) (bool, error) {
	var count int
	err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM articles_authors aa
		JOIN articles a ON a.id = aa.articles_id
		WHERE a.slug = ? AND aa.author_id = ?
	`, articleSlug, authorID).Scan(&count)
	return count > 0, err
}
