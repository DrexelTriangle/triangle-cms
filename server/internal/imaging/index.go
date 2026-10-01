package imaging

import (
	"context"
	"database/sql"
	"html"
	"log/slog"
	"math"
	"net/url"
	"regexp"
	"strconv"
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

// contentImageSrc finds the src of each <img> in article HTML. A regexp rather
// than a parser because the bodies are WordPress-era markup with shortcodes
// mixed in, and only the src attribute matters here.
var contentImageSrc = regexp.MustCompile(`(?i)<img\b[^>]*?\bsrc\s*=\s*["']([^"']+)["']`)

// wpDerivative matches the "-1024x683" WordPress put before the extension of
// its resized copies.
var wpDerivative = regexp.MustCompile(`-(\d+)x(\d+)(\.[A-Za-z0-9]+)$`)

// ForContent returns renditions for the images inside an article body, keyed by
// each image's wp-content path exactly as the body references it (URL-decoded,
// no host, no query), or nil when none have any.
//
// Article pages need this because their lead photo is usually inline in the
// body, not the featured image: the CMS editor inserts it there, and so did
// WordPress. About half of those references are WordPress's own resized copies
// (photo-1024x683.jpg) rather than the original. Those resolve to the
// original's renditions, but only when the aspect ratio agrees: WordPress also
// made square-cropped thumbnails, and swapping one of those for the uncropped
// photo would change what the reader sees, not just how sharp it is.
func (i *Index) ForContent(body string) map[string][]models.ImageVariant {
	if i == nil || body == "" {
		return nil
	}

	var out map[string][]models.ImageVariant
	for _, match := range contentImageSrc.FindAllStringSubmatch(body, -1) {
		key, ok := mediaPathFromURL(html.UnescapeString(match[1]))
		if !ok {
			continue
		}
		if _, seen := out[key]; seen {
			continue
		}

		variants := i.ForPath(key)
		if variants == nil {
			variants = i.forDerivative(key)
		}
		if variants == nil {
			continue
		}
		if out == nil {
			out = make(map[string][]models.ImageVariant)
		}
		out[key] = variants
	}
	return out
}

func (i *Index) forDerivative(mediaPath string) []models.ImageVariant {
	match := wpDerivative.FindStringSubmatch(mediaPath)
	if match == nil {
		return nil
	}
	width, _ := strconv.Atoi(match[1])
	height, _ := strconv.Atoi(match[2])
	if width <= 0 || height <= 0 {
		return nil
	}

	original := strings.TrimSuffix(mediaPath, match[0]) + match[3]
	variants := i.ForPath(original)
	if len(variants) == 0 {
		return nil
	}

	// Compare against the widest rendition: its dimensions are the least
	// affected by rounding. WordPress rounds the derivative's short side to a
	// whole pixel, so a 1024px copy of a 3:2 photo can be off by ~0.1%; a crop
	// is off by far more.
	widest := variants[len(variants)-1]
	if widest.Height <= 0 {
		return nil
	}
	want := float64(widest.Width) / float64(widest.Height)
	got := float64(width) / float64(height)
	if math.Abs(got-want)/want > 0.02 {
		return nil
	}
	return variants
}
