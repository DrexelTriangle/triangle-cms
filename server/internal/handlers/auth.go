package handlers

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"server/internal/auth"
	db "server/internal/database"
)

func setSessionCookie(w http.ResponseWriter, sessionID string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    sessionID,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
		MaxAge:   maxAge,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: "", HttpOnly: true, Path: "/", MaxAge: -1})
}

// @Summary Start OIDC login
// @Tags auth
// @Success 302 {object} models.AuthRedirectResponse
// @Router /v1/auth/login [get]
func AuthLogin(cfg auth.OIDCConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		verifier := auth.GenerateRandom()
		state := auth.GenerateRandom()

		http.SetCookie(w, &http.Cookie{
			Name:     "pkce",
			Value:    verifier + "." + state,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Path:     "/",
			MaxAge:   300,
		})

		q := url.Values{
			"client_id":             {cfg.ClientID},
			"redirect_uri":          {cfg.RedirectURL},
			"response_type":         {"code"},
			"scope":                 {"openid profile email"},
			"state":                 {state},
			"code_challenge":        {auth.PKCEChallenge(verifier)},
			"code_challenge_method": {"S256"},
		}
		http.Redirect(w, r, cfg.AuthURL+"?"+q.Encode(), http.StatusFound)
	}
}

// @Summary Complete OIDC login callback
// @Tags auth
// @Param code query string true "Authorization code"
// @Param state query string true "State"
// @Success 302 {object} models.AuthRedirectResponse
// @Router /v1/auth/callback [get]
func AuthCallback(cfg auth.OIDCConfig, verifier *oidc.IDTokenVerifier, conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		loginErr := func(reason string) {
			http.Redirect(w, r, cfg.FrontendURL+"/login?error="+reason, http.StatusFound)
		}

		pkceCookie, err := r.Cookie("pkce")
		if err != nil {
			loginErr("missing_state")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "pkce", Value: "", Path: "/", MaxAge: -1})

		parts := strings.SplitN(pkceCookie.Value, ".", 2)
		if len(parts) != 2 || r.URL.Query().Get("state") != parts[1] {
			loginErr("state_mismatch")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			loginErr("missing_code")
			return
		}

		tr, err := auth.ExchangeCode(r.Context(), cfg, code, parts[0])
		if err != nil {
			loginErr("token_exchange_failed")
			return
		}

		idToken, err := verifier.Verify(r.Context(), tr.IDToken)
		if err != nil {
			loginErr("invalid_token")
			return
		}

		var claims struct {
			Sub   string `json:"sub"`
			Email string `json:"email"`
			Name  string `json:"name"`
		}
		if err := idToken.Claims(&claims); err != nil {
			loginErr("invalid_claims")
			return
		}

		user, err := db.FindOrCreateUser(r.Context(), conn, claims.Sub, claims.Email, claims.Name)
		if err != nil {
			loginErr("user_error")
			return
		}

		sessionID, err := db.GenerateSessionID()
		if err != nil {
			loginErr("session_error")
			return
		}

		expiresIn := tr.ExpiresIn
		if expiresIn <= 0 {
			expiresIn = 3600
		}

		if err := db.CreateSession(r.Context(), conn, db.Session{
			ID:           sessionID,
			UserID:       user.ID,
			AccessToken:  tr.AccessToken,
			RefreshToken: tr.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
		}); err != nil {
			loginErr("session_error")
			return
		}

		setSessionCookie(w, sessionID, expiresIn)
		http.Redirect(w, r, cfg.FrontendURL+"/", http.StatusFound)
	}
}

// @Summary Logout current session
// @Tags auth
// @Success 204
// @Router /v1/auth/logout [post]
func AuthLogout(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("sid"); err == nil {
			_ = db.DeleteSession(r.Context(), conn, cookie.Value)
		}
		clearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
	}
}
