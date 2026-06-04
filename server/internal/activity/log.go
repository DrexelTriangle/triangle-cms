package activity

import (
	"log/slog"
	"net/http"
	"strings"

	"server/internal/middleware"
)

func LogRequest(r *http.Request, action, target string, args ...any) {
	attrs := []any{
		"kind", "activity",
		"action", strings.TrimSpace(action),
		"target", strings.TrimSpace(target),
	}

	if r != nil {
		attrs = append(attrs,
			"method", r.Method,
			"path", r.URL.Path,
		)
		if user, ok := middleware.UserFromContext(r.Context()); ok && user != nil {
			attrs = append(attrs,
				"actor_id", user.ID,
				"actor_name", user.Name,
				"actor_role", string(user.Role),
			)
		}
	}

	attrs = append(attrs, args...)
	slog.Info("activity event", attrs...)
}
