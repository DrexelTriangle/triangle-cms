package auth

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultSessionTTL = 7 * 24 * time.Hour

func SessionTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CMS_SESSION_TTL_SECONDS"))
	if raw == "" {
		return defaultSessionTTL
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		slog.Warn("invalid CMS_SESSION_TTL_SECONDS; using default", "value", raw, "default_seconds", int(defaultSessionTTL.Seconds()))
		return defaultSessionTTL
	}

	return time.Duration(seconds) * time.Second
}
