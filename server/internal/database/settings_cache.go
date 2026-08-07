package database

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cms_settings is read on nearly every public page render -- the site title and
// footer on every layout, the carousel and developing stories on the homepage --
// and written a few times a month by an editor. Each read was its own round
// trip, so a burst of traffic turned a handful of near-constant values into the
// dominant query load and, with a small connection pool, into a queue in front
// of the database. See the 2026-08-06 cutover outage.
//
// The cache is per-process and deliberately tiny: cms_settings holds under a
// dozen keys. Blue/green runs two processes, but only the active slot serves
// traffic, so the standby's cache is cold rather than wrong.

const defaultSettingsCacheTTL = 30 * time.Second

type settingsCacheEntry struct {
	raw     string
	found   bool
	expires time.Time
}

var (
	settingsCacheMu  sync.RWMutex
	settingsCacheMap = map[string]settingsCacheEntry{}
)

// settingsCacheTTL is read per call rather than cached in a package variable so
// a deployment can change it without a rebuild, matching DB_MAX_OPEN_CONNS.
// Setting it to 0 disables caching entirely, which is the escape hatch if a
// stale value is ever suspected of hiding a bug.
func settingsCacheTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("SETTINGS_CACHE_TTL_SECONDS"))
	if raw == "" {
		return defaultSettingsCacheTTL
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return defaultSettingsCacheTTL
	}
	return time.Duration(seconds) * time.Second
}

// readSettingRaw returns the stored value for a key and whether the row exists,
// which callers distinguish: a missing row falls back to a built-in default,
// while a present-but-blank one is a deliberate empty value in some cases.
func readSettingRaw(ctx context.Context, conn *sql.DB, key string) (string, bool, error) {
	ttl := settingsCacheTTL()
	if ttl > 0 {
		settingsCacheMu.RLock()
		entry, ok := settingsCacheMap[key]
		settingsCacheMu.RUnlock()
		if ok && time.Now().Before(entry.expires) {
			return entry.raw, entry.found, nil
		}
	}

	var raw string
	err := conn.QueryRowContext(ctx,
		"SELECT value_text FROM cms_settings WHERE key_name = ? LIMIT 1", key).Scan(&raw)
	switch {
	case err == sql.ErrNoRows:
		raw, err = "", nil
		storeSetting(key, raw, false, ttl)
		return "", false, nil
	case err != nil:
		// Errors are never cached: a failed read must not pin an empty value
		// for the whole TTL.
		return "", false, err
	}
	storeSetting(key, raw, true, ttl)
	return raw, true, nil
}

func storeSetting(key, raw string, found bool, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	settingsCacheMu.Lock()
	settingsCacheMap[key] = settingsCacheEntry{raw: raw, found: found, expires: time.Now().Add(ttl)}
	settingsCacheMu.Unlock()
}

// writeSettingRaw upserts a key and drops its cache entry, so an editor's save
// is visible on the next request rather than up to a TTL later. Every writer to
// cms_settings goes through here; an inline INSERT elsewhere would leave the
// cache holding the old value.
func writeSettingRaw(ctx context.Context, conn *sql.DB, key, value string) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO cms_settings (key_name, value_text)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value_text = VALUES(value_text)
	`, key, value)
	if err != nil {
		return err
	}
	InvalidateSettingCache(key)
	return nil
}

// InvalidateSettingCache drops one key. Exported so a writer that cannot use
// writeSettingRaw -- a migration, or a multi-statement transaction -- can still
// keep the cache honest.
func InvalidateSettingCache(key string) {
	settingsCacheMu.Lock()
	delete(settingsCacheMap, key)
	settingsCacheMu.Unlock()
}

// ResetSettingsCache drops every entry.
func ResetSettingsCache() {
	settingsCacheMu.Lock()
	settingsCacheMap = map[string]settingsCacheEntry{}
	settingsCacheMu.Unlock()
}
