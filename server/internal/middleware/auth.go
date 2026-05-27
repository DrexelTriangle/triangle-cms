package middleware

import (
	"context"
	"database/sql"
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

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":"` + msg + `"}`))
}

func RequireAuth(verifier *oidc.IDTokenVerifier, conn *sql.DB, oidcCfg auth.OIDCConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Session cookie (BFF path)
			if cookie, err := r.Cookie("sid"); err == nil && conn != nil {
				user, session, err := db.GetUserBySessionID(r.Context(), conn, cookie.Value)
				if err == nil {
					if time.Now().Before(session.ExpiresAt) {
						ctx := context.WithValue(r.Context(), contextKeyUser, user)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					// Try refresh
					if session.RefreshToken != "" {
						tr, err := auth.RefreshTokens(r.Context(), oidcCfg, session.RefreshToken)
						if err == nil {
							expiresIn := tr.ExpiresIn
							if expiresIn <= 0 {
								expiresIn = 3600
							}
							newExpiry := time.Now().Add(time.Duration(expiresIn) * time.Second)
							_ = db.UpdateSessionTokens(r.Context(), conn, session.ID, tr.AccessToken, tr.RefreshToken, newExpiry)
							http.SetCookie(w, &http.Cookie{
								Name: "sid", Value: session.ID, HttpOnly: true,
								SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: expiresIn,
							})
							ctx := context.WithValue(r.Context(), contextKeyUser, user)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
					_ = db.DeleteSession(r.Context(), conn, session.ID)
				}
				jsonError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			// Bearer token fallback (Swagger / API clients)
			if verifier == nil {
				jsonError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				jsonError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			token, err := verifier.Verify(r.Context(), strings.TrimPrefix(authHeader, "Bearer "))
			if err != nil {
				jsonError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			var claims struct {
				Sub   string `json:"sub"`
				Email string `json:"email"`
				Name  string `json:"name"`
			}
			if err := token.Claims(&claims); err != nil {
				jsonError(w, http.StatusUnauthorized, "invalid token claims")
				return
			}
			user, err := db.FindOrCreateUser(r.Context(), conn, claims.Sub, claims.Email, claims.Name)
			if err != nil {
				jsonError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			ctx := context.WithValue(r.Context(), contextKeyUser, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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
