package imaging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	db "server/internal/database"
)

// VariantsDir is where renditions live, MEDIA_ROOT-relative. It sits beside
// wp-content/uploads rather than inside it for two reasons: the media indexer
// walks uploads/ and would adopt every rendition as a new library item, and
// Nginx already serves all of /wp-content/, so no new location is needed.
const VariantsDir = "wp-content/variants"

const uploadsPrefix = "wp-content/uploads/"

// recipePattern bounds what a sidecar-reported recipe may look like, because it
// becomes a directory name under MEDIA_ROOT.
var recipePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)

// Reconciler keeps media_renditions in step with the media library.
//
// A background loop for the same reasons as the embedding reconciler: uploads,
// sideloads and the indexer's adoption of migrated files are three ways into
// the library, rendering on the upload path would make an editor wait on (and
// fail with) the sidecar, and only a loop converges the 13,000 originals that
// were already there. The first pass after deploy is the backfill.
type Reconciler struct {
	conn   *sql.DB
	client *Client
	root   string

	// checkStorage guards against writing into the empty directory Docker
	// creates when CephFS is not mounted. Renditions written there would be
	// recorded as done and then vanish when the mount returns.
	checkStorage func() error

	// Interval between passes once the library has converged. Short, unlike the
	// embedding reconciler's, because a fresh upload is usually about to go out
	// as a featured image and an idle pass is one cheap query.
	Interval time.Duration

	// BatchSize is how many images one pass renders before releasing the lock.
	BatchSize int

	// MaxSourceBytes skips originals the sidecar would refuse anyway, without
	// reading them into memory first.
	MaxSourceBytes int64

	// MaxAttempts is how many passes may fail on the same image, with the
	// sidecar otherwise healthy, before it is recorded as failed. Without a cap
	// one image that crashes the sidecar would stall the queue behind it.
	MaxAttempts int

	attempts map[int64]int
}

func NewReconciler(conn *sql.DB, client *Client, root string, checkStorage func() error) *Reconciler {
	return &Reconciler{
		conn:           conn,
		client:         client,
		root:           strings.TrimRight(strings.TrimSpace(root), "/"),
		checkStorage:   checkStorage,
		Interval:       time.Minute,
		BatchSize:      8,
		MaxSourceBytes: 100 << 20,
		MaxAttempts:    3,
		attempts:       make(map[int64]int),
	}
}

// Run blocks until ctx is cancelled. With no sidecar or no media root it logs
// once and returns, and the site keeps serving originals.
func (r *Reconciler) Run(ctx context.Context) {
	if !r.client.Enabled() {
		slog.Info("image reconciler disabled; no sidecar configured")
		return
	}
	if r.root == "" {
		slog.Info("image reconciler disabled; MEDIA_ROOT is not set")
		return
	}

	for {
		worked, err := r.pass(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("image reconciler pass failed", "error", err)
		}

		delay := r.Interval
		if worked && err == nil {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// reconcilerLockName serializes reconcilers across the blue and green slots,
// which are both live during a deploy.
const reconcilerLockName = "cms_image_reconciler"

func (r *Reconciler) pass(ctx context.Context) (bool, error) {
	lockConn, err := r.conn.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer lockConn.Close()

	var acquired sql.NullInt64
	if err := lockConn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", reconcilerLockName).Scan(&acquired); err != nil {
		return false, err
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return false, nil
	}
	defer func() {
		_, _ = lockConn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", reconcilerLockName)
	}()

	if r.checkStorage != nil {
		if err := r.checkStorage(); err != nil {
			return false, err
		}
	}

	recipe, err := r.client.Recipe(ctx)
	if err != nil {
		return false, err
	}
	if err := validateRecipe(recipe); err != nil {
		return false, err
	}

	orphans, err := db.OrphanedRenditions(ctx, r.conn, 100)
	if err != nil {
		return false, err
	}
	for _, orphan := range orphans {
		r.removeFiles(orphan.Variants, nil)
		if err := db.DeleteRendition(ctx, r.conn, orphan.MediaID); err != nil {
			return false, err
		}
	}
	if len(orphans) > 0 {
		slog.Info("removed renditions of deleted media", "count", len(orphans))
	}

	sources, err := db.MediaNeedingRenditions(ctx, r.conn, recipe.Name, r.BatchSize)
	if err != nil {
		return false, err
	}

	rendered, failed := 0, 0
	for _, source := range sources {
		variants, err := r.render(ctx, recipe, source)
		var unprocessable *UnprocessableError
		switch {
		case err == nil:
		case errors.As(err, &unprocessable), errors.Is(err, errUnusableSource):
			if err := r.fail(ctx, recipe, source, err); err != nil {
				return rendered > 0, err
			}
			failed++
			continue
		case ctx.Err() != nil:
			return rendered > 0, ctx.Err()
		default:
			// The sidecar answered /health at the top of this pass, so a
			// failure now is more likely this image than an outage. Count it,
			// and give up on the image once it has used its attempts.
			r.attempts[source.MediaID]++
			if r.attempts[source.MediaID] < r.MaxAttempts {
				return rendered > 0, fmt.Errorf("media %d (%s): %w", source.MediaID, source.Path, err)
			}
			if err := r.fail(ctx, recipe, source, err); err != nil {
				return rendered > 0, err
			}
			failed++
			continue
		}

		if err := db.SaveRenditions(ctx, r.conn, source.MediaID, recipe.Name, variants); err != nil {
			return rendered > 0, err
		}
		delete(r.attempts, source.MediaID)
		r.removeFiles(source.Previous, variants)
		rendered++
	}

	if rendered+failed > 0 {
		slog.Info("rendered media", "count", rendered, "failed", failed, "recipe", recipe.Name)
	}
	return len(sources) > 0, nil
}

func validateRecipe(recipe Recipe) error {
	if !recipePattern.MatchString(recipe.Name) {
		return fmt.Errorf("imaging: sidecar recipe %q is not a safe directory name", recipe.Name)
	}
	if recipe.Format != "webp" {
		return fmt.Errorf("imaging: sidecar renders %q; only webp is supported", recipe.Format)
	}
	if len(recipe.Widths) == 0 {
		return fmt.Errorf("imaging: sidecar recipe %q has no widths", recipe.Name)
	}
	for i, width := range recipe.Widths {
		if width <= 0 || (i > 0 && width <= recipe.Widths[i-1]) {
			return fmt.Errorf("imaging: sidecar widths %v must be positive and ascending", recipe.Widths)
		}
	}
	return nil
}

// errUnusableSource marks a media row whose original cannot be sent at all.
var errUnusableSource = errors.New("unusable source")

// render produces the full width ladder for one original and writes it to disk.
// Every width is rendered before anything is written, so a failure partway
// leaves no files that the database does not know about.
func (r *Reconciler) render(ctx context.Context, recipe Recipe, source db.RenditionSource) ([]db.RenditionVariant, error) {
	abs, ok := resolveWithin(r.root, source.Path)
	if !ok {
		return nil, fmt.Errorf("%w: path escapes the media root", errUnusableSource)
	}
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: original is missing from the media root", errUnusableSource)
	}
	if err != nil {
		return nil, err
	}
	if info.Size() > r.MaxSourceBytes {
		return nil, fmt.Errorf("%w: original is %d bytes, over the %d limit", errUnusableSource, info.Size(), r.MaxSourceBytes)
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}

	type pending struct {
		variant db.RenditionVariant
		data    []byte
	}
	var out []pending
	for _, width := range recipe.Widths {
		result, err := r.client.Render(ctx, src, width)
		if err != nil {
			return nil, err
		}
		// The sidecar never enlarges. Once it returns less than was asked for,
		// that is the original's own width, and every wider step would be the
		// same image again.
		if len(out) == 0 || result.Width > out[len(out)-1].variant.Width {
			out = append(out, pending{
				variant: db.RenditionVariant{
					Path:      variantPath(recipe.Name, source.Path, result.Width),
					Width:     result.Width,
					Height:    result.Height,
					SizeBytes: int64(len(result.Data)),
				},
				data: result.Data,
			})
		}
		if result.Width < width {
			break
		}
	}

	variants := make([]db.RenditionVariant, 0, len(out))
	for _, item := range out {
		if err := r.writeFile(item.variant.Path, item.data); err != nil {
			return nil, err
		}
		variants = append(variants, item.variant)
	}
	return variants, nil
}

func (r *Reconciler) fail(ctx context.Context, recipe Recipe, source db.RenditionSource, cause error) error {
	delete(r.attempts, source.MediaID)
	slog.Warn("could not render media; it will keep serving the original",
		"media_id", source.MediaID, "path", source.Path, "error", cause)
	return db.SaveRenditionFailure(ctx, r.conn, source.MediaID, recipe.Name, source.Previous, cause.Error())
}

// variantPath maps an original to one of its renditions:
//
//	wp-content/uploads/2026/08/staff.jpg -> wp-content/variants/<recipe>/2026/08/staff.jpg.960w.webp
//
// The recipe is in the path because Cloudflare caches these for 30 days as
// immutable. A recipe change has to produce new URLs, not new bytes at old ones.
// The original's extension stays in the name so staff.jpg and staff.png, which
// both exist in the migrated corpus, cannot collide.
func variantPath(recipe, mediaPath string, width int) string {
	rel := strings.TrimPrefix(path.Clean("/"+mediaPath), "/")
	rel = strings.TrimPrefix(rel, uploadsPrefix)
	return path.Join(VariantsDir, recipe, rel) + "." + strconv.Itoa(width) + "w.webp"
}

// writeFile stores one rendition atomically: a partially written file is never
// visible to Nginx, and a rerun overwrites a leftover from an interrupted pass.
func (r *Reconciler) writeFile(relPath string, data []byte) error {
	abs, ok := resolveWithin(r.root, relPath)
	if !ok {
		return fmt.Errorf("rendition path %q escapes the media root", relPath)
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".rendition-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	// CreateTemp makes 0600 files, and Nginx runs as another user.
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, abs)
}

// removeFiles deletes the variants in old that are not also in keep. Failures
// are logged, not returned: a leftover file costs some disk, nothing more.
func (r *Reconciler) removeFiles(old, keep []db.RenditionVariant) {
	kept := make(map[string]struct{}, len(keep))
	for _, variant := range keep {
		kept[variant.Path] = struct{}{}
	}
	for _, variant := range old {
		if _, ok := kept[variant.Path]; ok {
			continue
		}
		// Only ever delete inside the variants tree, whatever a row says. The
		// check is on the cleaned path: "wp-content/variants/../uploads/x.jpg"
		// starts with the right prefix and names an original.
		cleaned := strings.TrimPrefix(path.Clean("/"+variant.Path), "/")
		if !strings.HasPrefix(cleaned, VariantsDir+"/") {
			continue
		}
		abs, ok := resolveWithin(r.root, cleaned)
		if !ok {
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("could not remove rendition", "path", variant.Path, "error", err)
		}
	}
}

// resolveWithin joins a MEDIA_ROOT-relative path onto root, refusing anything
// that would land outside it.
func resolveWithin(root, relPath string) (string, bool) {
	cleaned := path.Clean("/" + strings.TrimSpace(relPath))
	if cleaned == "/" {
		return "", false
	}
	abs := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	if !strings.HasPrefix(abs, filepath.Clean(root)+string(os.PathSeparator)) {
		return "", false
	}
	return abs, true
}
