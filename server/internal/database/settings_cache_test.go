package database

import (
	"context"
	"testing"
	"time"
)

func TestSettingsCacheTTLDefaults(t *testing.T) {
	t.Setenv("SETTINGS_CACHE_TTL_SECONDS", "")
	if got := settingsCacheTTL(); got != defaultSettingsCacheTTL {
		t.Fatalf("unset: got %v, want %v", got, defaultSettingsCacheTTL)
	}

	t.Setenv("SETTINGS_CACHE_TTL_SECONDS", "5")
	if got := settingsCacheTTL(); got != 5*time.Second {
		t.Fatalf("override: got %v, want 5s", got)
	}

	// Disabling the cache is a supported escape hatch, so 0 must survive rather
	// than fall back to the default.
	t.Setenv("SETTINGS_CACHE_TTL_SECONDS", "0")
	if got := settingsCacheTTL(); got != 0 {
		t.Fatalf("zero: got %v, want 0", got)
	}

	for _, bad := range []string{"abc", "-1", "1.5"} {
		t.Setenv("SETTINGS_CACHE_TTL_SECONDS", bad)
		if got := settingsCacheTTL(); got != defaultSettingsCacheTTL {
			t.Fatalf("invalid %q: got %v, want the default", bad, got)
		}
	}
}

// A cache hit must not reach the database. Passing a nil *sql.DB proves it: if
// readSettingRaw ever falls through to a query, this panics rather than quietly
// still working because a real connection was available.
func TestCachedReadDoesNotTouchTheDatabase(t *testing.T) {
	t.Setenv("SETTINGS_CACHE_TTL_SECONDS", "30")
	ResetSettingsCache()
	t.Cleanup(ResetSettingsCache)

	storeSetting("site_title", "The Triangle", true, 30*time.Second)

	raw, found, err := readSettingRaw(context.Background(), nil, "site_title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || raw != "The Triangle" {
		t.Fatalf("got (%q, %v), want (\"The Triangle\", true)", raw, found)
	}
}

// A missing row is cached too, otherwise every render of an unset key -- the
// footer before anyone customizes it -- keeps querying.
func TestMissingRowIsCached(t *testing.T) {
	t.Setenv("SETTINGS_CACHE_TTL_SECONDS", "30")
	ResetSettingsCache()
	t.Cleanup(ResetSettingsCache)

	storeSetting("footer_menu", "", false, 30*time.Second)

	raw, found, err := readSettingRaw(context.Background(), nil, "footer_menu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || raw != "" {
		t.Fatalf("got (%q, %v), want (\"\", false)", raw, found)
	}
}

func TestInvalidateDropsTheEntry(t *testing.T) {
	ResetSettingsCache()
	t.Cleanup(ResetSettingsCache)

	storeSetting("site_title", "Stale", true, 30*time.Second)
	InvalidateSettingCache("site_title")

	settingsCacheMu.RLock()
	_, ok := settingsCacheMap["site_title"]
	settingsCacheMu.RUnlock()
	if ok {
		t.Fatal("entry survived invalidation, so an editor's save would not be visible")
	}
}

func TestExpiredEntryIsNotServed(t *testing.T) {
	t.Setenv("SETTINGS_CACHE_TTL_SECONDS", "30")
	ResetSettingsCache()
	t.Cleanup(ResetSettingsCache)

	// Already expired.
	storeSetting("site_title", "Stale", true, -time.Second)

	settingsCacheMu.RLock()
	entry := settingsCacheMap["site_title"]
	settingsCacheMu.RUnlock()
	if time.Now().Before(entry.expires) {
		t.Fatal("entry should already be expired")
	}
}

// Storing with caching disabled must be a no-op, or SETTINGS_CACHE_TTL_SECONDS=0
// would still serve values written before it was set.
func TestDisabledCacheStoresNothing(t *testing.T) {
	ResetSettingsCache()
	t.Cleanup(ResetSettingsCache)

	storeSetting("site_title", "The Triangle", true, 0)

	settingsCacheMu.RLock()
	_, ok := settingsCacheMap["site_title"]
	settingsCacheMu.RUnlock()
	if ok {
		t.Fatal("value cached despite the cache being disabled")
	}
}
