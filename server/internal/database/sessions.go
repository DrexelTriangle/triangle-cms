package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"time"

	"server/internal/models"
)

func EnsureSessionsTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cms_sessions (
			id            VARCHAR(64) PRIMARY KEY,
			user_id       BIGINT UNSIGNED NOT NULL,
			access_token  TEXT NOT NULL,
			refresh_token TEXT NOT NULL DEFAULT '',
			expires_at    DATETIME NOT NULL,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_cms_sessions_user_id (user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
	`)
	return err
}

type Session struct {
	ID           string
	UserID       int64
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

func GenerateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func CreateSession(ctx context.Context, conn *sql.DB, s Session) error {
	_, err := conn.ExecContext(ctx,
		"INSERT INTO cms_sessions (id, user_id, access_token, refresh_token, expires_at) VALUES (?, ?, ?, ?, ?)",
		s.ID, s.UserID, s.AccessToken, s.RefreshToken, s.ExpiresAt.UTC().Format("2006-01-02 15:04:05"),
	)
	return err
}

func GetSessionByID(ctx context.Context, conn *sql.DB, id string) (Session, error) {
	var s Session
	var expiresAtRaw any
	err := conn.QueryRowContext(ctx,
		"SELECT id, user_id, access_token, refresh_token, expires_at FROM cms_sessions WHERE id = ?",
		id,
	).Scan(&s.ID, &s.UserID, &s.AccessToken, &s.RefreshToken, &expiresAtRaw)
	if err != nil {
		return Session{}, err
	}

	switch v := expiresAtRaw.(type) {
	case time.Time:
		s.ExpiresAt = v.UTC()
	case []byte:
		expiresAt, err := time.Parse("2006-01-02 15:04:05", string(v))
		if err != nil {
			return Session{}, err
		}
		s.ExpiresAt = expiresAt.UTC()
	case string:
		expiresAt, err := time.Parse("2006-01-02 15:04:05", v)
		if err != nil {
			return Session{}, err
		}
		s.ExpiresAt = expiresAt.UTC()
	default:
		return Session{}, sql.ErrNoRows
	}
	return s, nil
}

func UpdateSessionTokens(ctx context.Context, conn *sql.DB, id, accessToken, refreshToken string, expiresAt time.Time) error {
	_, err := conn.ExecContext(ctx,
		"UPDATE cms_sessions SET access_token = ?, refresh_token = ?, expires_at = ? WHERE id = ?",
		accessToken, refreshToken, expiresAt.UTC().Format("2006-01-02 15:04:05"), id,
	)
	return err
}

func DeleteSession(ctx context.Context, conn *sql.DB, id string) error {
	_, err := conn.ExecContext(ctx, "DELETE FROM cms_sessions WHERE id = ?", id)
	return err
}

func GetUserBySessionID(ctx context.Context, conn *sql.DB, sessionID string) (*models.User, Session, error) {
	session, err := GetSessionByID(ctx, conn, sessionID)
	if err != nil {
		return nil, Session{}, err
	}
	user, err := GetUserByID(ctx, conn, session.UserID)
	if err != nil {
		return nil, Session{}, err
	}
	return user, session, nil
}
