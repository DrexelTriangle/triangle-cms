package imaging

import (
	"context"
	"database/sql"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	db "server/internal/database"
	"server/internal/models"
)

// Index answers "which renditions does this image have?" from memory.
//
// Article lists are the hot path of the public API: the homepage alone builds
// several of them. A query per list, or worse per article, to attach variants
// would add round trips to every one. The whole rendition table is a few MB, so
// each backend keeps a copy and reloads it on a timer. A rendition becomes
// visible within one Interval of being written, which is far shorter than the
// public cache in front of the API anyway.
type Index struct {
	conn    *sql.DB
	baseURL string

	// Interval between reloads.
	Interval time.Duration

	mu     sync.RWMutex
	byPath map[string][]models.ImageVariant
}

// NewIndex returns an empty index. baseURL is MEDIA_BASE_URL, so rendition URLs
// are built exactly the way original URLs are.
func NewIndex(conn *sql.DB, baseURL string) *Index {
	return &Index{
		conn:     conn,
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Interval: time.Minute,
	}
}

// Run reloads the index until ctx is cancelled. A failed reload keeps serving
// the previous copy: stale variants still exist on disk, so they are still
// correct to advertise.
func (i *Index) Run(ctx context.Context) {
	for {
		if err := i.Refresh(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("could not reload image renditions; serving the previous copy", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(i.Interval):
		}
	}
}

// Refresh reloads the index from the database.
func (i *Index) Refresh(ctx context.Context) error {
	if i.conn == nil {
		return nil
	}
	rows, err := db.RenditionsByMediaPath(ctx, i.conn)
	if err != nil {
		return err
	}

	byPath := make(map[string][]models.ImageVariant, len(rows))
	for mediaPath, variants := range rows {
		out := make([]models.ImageVariant, 0, len(variants))
		for _, variant := range variants {
			out = append(out, models.ImageVariant{
				URL:    i.url(variant.Path),
				Width:  variant.Width,
				Height: variant.Height,
			})
		}
		byPath[mediaPath] = out
	}

	i.mu.Lock()
	i.byPath = byPath
	i.mu.Unlock()
	return nil
}

func (i *Index) url(relPath string) string {
	if i.baseURL != "" {
		return i.baseURL + "/" + relPath
	}
	return "/" + relPath
}

// ForPath returns the renditions of the media item at a MEDIA_ROOT-relative
// path, narrowest first, or nil. The slice is shared: callers must not modify
// it.
func (i *Index) ForPath(mediaPath string) []models.ImageVariant {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.byPath[mediaPath]
}

// ForURL returns the renditions of the image an article's featured_image URL
// points at, or nil. Article image URLs are stored absolute and have carried
// more than one host over the years (the WordPress origin, then Delta), so the
// match is on the wp-content path alone.
func (i *Index) ForURL(imageURL string) []models.ImageVariant {
	if i == nil || imageURL == "" {
		return nil
	}
	mediaPath, ok := mediaPathFromURL(imageURL)
	if !ok {
		return nil
	}
	return i.ForPath(mediaPath)
}

func mediaPathFromURL(imageURL string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil {
		return "", false
	}
	at := strings.Index(parsed.Path, "/wp-content/")
	if at < 0 {
		return "", false
	}
	return parsed.Path[at+1:], true
}
