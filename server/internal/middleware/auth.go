package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"server/internal/auth"
	db "server/internal/database"
	"server/internal/models"
)

type contextKey string

const contextKeyUser contextKey = "cms_user"

func UserFromContext(ctx context.Context) (*models.User, bool) {
	u, ok := ctx.Value(contextKeyUser).(*models.User)
	return u, ok
}

// ContextWithUser attaches an identified user to ctx. The context key is
// unexported, so this is also how tests stand up an authenticated request
// without a live OIDC flow.
func ContextWithUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, contextKeyUser, user)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}

func RequireAuth(verifier *oidc.IDTokenVerifier, conn *sql.DB, oidcCfg auth.OIDCConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, status, msg := resolveUser(w, r, verifier, conn, oidcCfg)
			if status != 0 {
				jsonError(w, status, msg)
				return
			}
			ctx := ContextWithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth identifies the caller when it can and lets them through either
// way. It exists for endpoints that are public but return MORE to a logged-in
// editor than to an anonymous reader — the handler branches on
// UserFromContext rather than the middleware rejecting the request. An invalid
// or expired credential is treated as anonymous rather than as an error, so a
// stale cookie degrades to the public view instead of breaking the page.
func OptionalAuth(verifier *oidc.IDTokenVerifier, conn *sql.DB, oidcCfg auth.OIDCConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _, _ := resolveUser(w, r, verifier, conn, oidcCfg)
			if user == nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := ContextWithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveUser identifies the caller from a session cookie or bearer token. It
// returns a zero status on success; otherwise the status/message the caller
// should reject with. It may write a refreshed session cookie to w, so it must
// be called before anything else writes the response.
func resolveUser(w http.ResponseWriter, r *http.Request, verifier *oidc.IDTokenVerifier, conn *sql.DB, oidcCfg auth.OIDCConfig) (*models.User, int, string) {
	// Session cookie (BFF path). A present cookie is a deliberate claim of
	// identity, so it is never silently fallen back to bearer: if it does not
	// resolve, the caller is rejected.
	if cookie, err := r.Cookie("sid"); err == nil && conn != nil {
		user, session, err := db.GetUserBySessionID(r.Context(), conn, cookie.Value)
		if err == nil {
			if time.Now().Before(session.ExpiresAt) {
				return user, 0, ""
			}
			// Try refresh
			if session.RefreshToken != "" {
				tr, err := auth.RefreshTokens(r.Context(), oidcCfg, session.RefreshToken)
				if err == nil {
					newRefreshToken := strings.TrimSpace(tr.RefreshToken)
					if newRefreshToken == "" {
						newRefreshToken = session.RefreshToken
					}
					sessionTTL := auth.SessionTTL()
					sessionMaxAge := int(sessionTTL.Seconds())
					newExpiry := time.Now().Add(sessionTTL)
					_ = db.UpdateSessionTokens(r.Context(), conn, session.ID, tr.AccessToken, newRefreshToken, newExpiry)
					http.SetCookie(w, &http.Cookie{
						Name: "sid", Value: session.ID, HttpOnly: true,
						SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: sessionMaxAge,
					})
					return user, 0, ""
				}
				slog.Warn("auth: refresh failed", "path", r.URL.Path, "error", err)
			}
			slog.Warn("auth: deleting expired/unrefreshable session", "path", r.URL.Path, "session_id", session.ID)
			_ = db.DeleteSession(r.Context(), conn, session.ID)
		} else {
			slog.Warn("auth: session lookup failed", "path", r.URL.Path, "error", err)
		}
		return nil, http.StatusUnauthorized, "unauthorized"
	}

	// Bearer token fallback (Swagger / API clients)
	if verifier == nil {
		return nil, http.StatusUnauthorized, "unauthorized"
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, http.StatusUnauthorized, "unauthorized"
	}
	token, err := verifier.Verify(r.Context(), strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		return nil, http.StatusUnauthorized, "invalid token"
	}
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, http.StatusUnauthorized, "invalid token claims"
	}
	user, err := db.FindOrCreateUser(r.Context(), conn, claims.Sub, claims.Email, claims.Name)
	if err != nil {
		return nil, http.StatusInternalServerError, "internal server error"
	}
	return user, 0, ""
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok || user.Role != models.RoleAdmin {
			jsonError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}
