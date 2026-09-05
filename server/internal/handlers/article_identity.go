package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	db "server/internal/database"
	"server/internal/models"
	"strconv"
	"strings"
)

type articleTarget struct {
	slug  string
	id    int64
	hasID bool
}

func articleTargetFromRequest(r *http.Request) (articleTarget, error) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if !isValidCanonicalSlug(slug) {
		return articleTarget{}, fmt.Errorf("slug must be canonical")
	}
	rawID := strings.TrimSpace(r.URL.Query().Get("id"))
	if rawID == "" {
		return articleTarget{slug: slug}, nil
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		return articleTarget{}, fmt.Errorf("id must be a positive integer")
	}
	return articleTarget{slug: slug, id: id, hasID: true}, nil
}

// resolveArticleTarget pins a slug-only request to one row before anything reads
// or locks. Slugs are not unique, so without this the same request could lock
// one duplicate, read a second and write a third; the legacy /articles/:slug
// links that predate the id-qualified routes all arrive this way. The archived
// flag says which state the caller works in, so a restore resolves the archived
// twin and a save resolves the live one.
//
// A slug matching no row leaves the target unresolved. The handler's own query
// then reports the 404 on its row count, which is the same answer it gave before.
func resolveArticleTarget(ctx context.Context, conn *sql.DB, target articleTarget, archived bool) (articleTarget, error) {
	if target.hasID {
		return target, nil
	}
	id, err := db.ResolveArticleIDBySlug(ctx, conn, target.slug, archived)
	if err != nil {
		return target, err
	}
	if id == 0 {
		return target, nil
	}
	target.id = id
	target.hasID = true
	return target, nil
}

func (target articleTarget) lockKey() string {
	if target.hasID {
		return fmt.Sprintf("id:%d", target.id)
	}
	return "slug:" + target.slug
}

func (target articleTarget) articleWhere() (string, []any) {
	if target.hasID {
		return "`id` = ? AND `slug` = ?", []any{target.id, target.slug}
	}
	return "`slug` = ?", []any{target.slug}
}

func (target articleTarget) articleWhereArchived(archived bool) (string, []any) {
	where, args := target.articleWhere()
	if archived {
		return where + " AND `archived_at` IS NOT NULL", args
	}
	return where + " AND `archived_at` IS NULL", args
}

// errArticleSlugLockBusy separates "another create holds this slug" from a
// broken database, so the caller can answer 503 and invite a retry instead of
// reporting a server fault.
var errArticleSlugLockBusy = errors.New("timed out waiting for the article slug lock")

func articleCreateDBLockName(candidate string) string {
	// MySQL caps a lock name at 64 characters and slugs in the corpus run to
	// 154, so the name is a hash rather than the slug itself. A collision only
	// serializes two unrelated creates, which is the behaviour we want anyway.
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(candidate))
	return "article-create:" + strconv.FormatUint(hash.Sum64(), 36)
}

func acquireArticleCreateDBLock(ctx context.Context, conn *sql.Conn, candidate string) (func(), error) {
	lockName := articleCreateDBLockName(candidate)
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lockName).Scan(&acquired); err != nil {
		return nil, err
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return nil, errArticleSlugLockBusy
	}
	return func() {
		var released sql.NullInt64
		_ = conn.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
	}, nil
}

// reserveArticleSlug returns a free slug and the lock that keeps it free until
// the insert lands.
//
// The lock is held on the CANDIDATE, not on the stem it was derived from. Two
// creates can reach the same candidate from different stems (one titled "Foo"
// whose slug becomes foo-2, one whose slug is literally foo-2) and a lock on
// the stem would let those two run concurrently and both insert foo-2. The
// candidate is the thing that has to be unique, so the candidate is the thing
// that is locked.
func reserveArticleSlug(ctx context.Context, conn *sql.Conn, requested, title string) (string, func(), error) {
	base := db.ArticleSlugBase(requested, title)
	for suffix := 1; suffix <= 100; suffix++ {
		candidate := base
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		release, err := acquireArticleCreateDBLock(ctx, conn, candidate)
		if err != nil {
			return "", nil, err
		}
		taken, err := db.ArticleSlugExists(ctx, conn, candidate)
		if err != nil {
			release()
			return "", nil, err
		}
		if !taken {
			return candidate, release, nil
		}
		release()
	}
	return "", nil, fmt.Errorf("every slug from %q to %q is already taken", base, fmt.Sprintf("%s-%d", base, 100))
}

// insertArticleWithUniqueSlug holds a pooled connection for exactly as long as
// the slug reservation and the insert need it.
//
// The connection is dedicated because GET_LOCK is scoped to one, and it is
// released here rather than deferred to the end of the request because
// everything the create does afterwards (authors, categories, taxonomy counts)
// goes back to the pool. A handler that held this connection across those
// calls would be waiting on the pool while holding part of it, which deadlocks
// outright once the pool is the single connection the integration tests use.
func insertArticleWithUniqueSlug(ctx context.Context, conn *sql.DB, body *models.ArticleInput) (int64, error) {
	lockedConn, err := conn.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer lockedConn.Close()

	slug, releaseSlug, err := reserveArticleSlug(ctx, lockedConn, body.Slug, body.Title)
	if err != nil {
		return 0, err
	}
	defer releaseSlug()
	body.Slug = slug

	result, err := db.Insert(ctx, lockedConn, "articles",
		[]string{"title", "slug", "description", "text", "excerpt", "categories", "pub_date", "mod_date", "priority", "breaking_news", "comment_status", "photo_url", "tags", "metadata", "focus_keyword", "meta_description", "seo_title", "creation_date", "scheduled_pub_date", "canonical_url", "noindex", "photo_alt"},
		db.ArticleInputToDBFields(*body)...,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// rejectTakenSlug answers 409 when a rename would land on a slug another article
// already holds. Creation dedupes silently because it picked the slug itself; a
// rename carries a slug the editor typed, and quietly storing a different one
// would leave them looking at a permalink the site does not serve.
func rejectTakenSlug(w http.ResponseWriter, r *http.Request, conn *sql.DB, target articleTarget, newSlug string) bool {
	if newSlug == "" || newSlug == target.slug {
		return false
	}
	// An unresolved target is a slug that matches no article. That is a 404, and
	// the caller's own write reports it as one; answering 409 here would explain
	// the wrong problem.
	if !target.hasID {
		return false
	}
	taken, err := db.ArticleSlugTakenByOther(r.Context(), conn, newSlug, target.id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return true
	}
	if taken {
		writeError(w, http.StatusConflict, "slug is already taken by another article")
		return true
	}
	return false
}
