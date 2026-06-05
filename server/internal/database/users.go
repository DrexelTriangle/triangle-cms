package database

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"

	"server/internal/models"
)

func EnsureArticlesSchema(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		ALTER TABLE articles
		ADD COLUMN IF NOT EXISTS archived_at DATETIME NULL DEFAULT NULL
	`)
	return err
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
	autoPromoteAllAdmins := strings.EqualFold(strings.TrimSpace(os.Getenv("CMS_AUTO_PROMOTE_ALL_ADMINS")), "true")
	user, err := findUserBySub(ctx, conn, sub)
	if err == nil {
		if autoPromoteAllAdmins && user.Role != models.RoleAdmin {
			if err := UpdateUserRole(ctx, conn, user.ID, models.RoleAdmin); err == nil {
				user.Role = models.RoleAdmin
			}
		}
		_ = updateLastLogin(ctx, conn, user.ID)
		return user, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	authorID := findAuthorIDByEmail(ctx, conn, email)

	role := models.RoleEditor
	if autoPromoteAllAdmins {
		role = models.RoleAdmin
	}

	res, err := conn.ExecContext(ctx,
		"INSERT INTO cms_users (sub, email, name, role, author_id) VALUES (?, ?, ?, ?, ?)",
		sub, email, name, role, authorID,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
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
