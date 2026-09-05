package activity

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

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

	store := DefaultStore()
	if store == nil {
		return
	}
	// Use slog's argument normalization for the same attribute values as stdout.
	record := slog.NewRecord(time.Now().UTC(), slog.LevelInfo, "activity event", 0)
	record.Add(attrs...)
	values := map[string]any{"service": "cms"}
	record.Attrs(func(attr slog.Attr) bool {
		values[attr.Key] = attr.Value.Resolve().Any()
		return true
	})
	entry := Entry{
		Timestamp: record.Time, Level: "INFO", Message: record.Message,
		Kind: "activity", Attributes: values,
	}
	entry.Action, _ = values["action"].(string)
	entry.Target, _ = values["target"].(string)
	entry.ActorID, _ = values["actor_id"].(int64)
	entry.ActorName, _ = values["actor_name"].(string)
	entry.ActorRole, _ = values["actor_role"].(string)
	entry.Method, _ = values["method"].(string)
	entry.Path, _ = values["path"].(string)
	status, _ := values["status"].(int64)
	entry.Status = int(status)
	// A disconnected client must not cancel the audit of a completed mutation.
	if err := store.Write(context.Background(), entry); err != nil {
		slog.Error("failed to persist activity", "action", entry.Action, "error", err)
	}
}
