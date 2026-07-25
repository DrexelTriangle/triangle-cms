package handlers

import (
	"sync"
	"time"
)

// articleLocks provides per-slug mutual exclusion so that concurrent edits to
// the same article are serialized instead of racing on the row and its derived
// taxonomy counts. Locks are keyed by slug and reference-counted so idle slugs
// don't accumulate in memory.
type articleLockRegistry struct {
	mu    sync.Mutex
	locks map[string]*articleLock
}

type articleLock struct {
	mu   sync.Mutex
	refs int
}

func newArticleLockRegistry() *articleLockRegistry {
	return &articleLockRegistry{locks: make(map[string]*articleLock)}
}

// Lock acquires the mutex for the given slug and returns an unlock function that
// must be called to release it.
func (r *articleLockRegistry) Lock(slug string) func() {
	r.mu.Lock()
	l, ok := r.locks[slug]
	if !ok {
		l = &articleLock{}
		r.locks[slug] = l
	}
	l.refs++
	r.mu.Unlock()

	l.mu.Lock()

	return func() {
		l.mu.Unlock()

		r.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(r.locks, slug)
		}
		r.mu.Unlock()
	}
}

// articleEditLocks is the process-wide registry guarding article mutations.
var articleEditLocks = newArticleLockRegistry()

// articleEditLeaseTTL is how long an editing lease stays valid without a
// heartbeat. The frontend refreshes well within this window; once it lapses
// (e.g. the editor closed the tab without releasing), the lease is treated as
// free so the article isn't locked forever.
const articleEditLeaseTTL = 90 * time.Second

// articleEditLease records who currently holds the advisory editing lease for
// an article and when it expires.
type articleEditLease struct {
	HolderID   int64
	HolderName string
	ExpiresAt  time.Time
}

// articleEditLeaseRegistry tracks advisory "someone is editing this" leases,
// keyed by slug. It is separate from articleLockRegistry: the lock serializes
// the brief write path, while a lease spans a human's whole editing session so
// a second editor is warned before they start rather than at save time.
type articleEditLeaseRegistry struct {
	mu     sync.Mutex
	leases map[string]articleEditLease
}

func newArticleEditLeaseRegistry() *articleEditLeaseRegistry {
	return &articleEditLeaseRegistry{leases: make(map[string]articleEditLease)}
}

// Acquire grants or refreshes the lease for slug to the given holder. It returns
// the current lease and whether the caller now holds it. When another holder's
// lease is still valid, the caller is not granted the lease and the existing
// holder's lease is returned so the caller can be told who to wait for.
func (r *articleEditLeaseRegistry) Acquire(slug string, holderID int64, holderName string) (articleEditLease, bool) {
	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweepExpiredLocked(now)

	if existing, ok := r.leases[slug]; ok && existing.HolderID != holderID && existing.ExpiresAt.After(now) {
		return existing, false
	}

	lease := articleEditLease{
		HolderID:   holderID,
		HolderName: holderName,
		ExpiresAt:  now.Add(articleEditLeaseTTL),
	}
	r.leases[slug] = lease
	return lease, true
}

// Release drops the lease for slug only if it is held by holderID, so a stale
// release from a previous holder can't free a lease someone else has taken over.
func (r *articleEditLeaseRegistry) Release(slug string, holderID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.leases[slug]; ok && existing.HolderID == holderID {
		delete(r.leases, slug)
	}
}

// sweepExpiredLocked removes lapsed leases so abandoned slugs don't accumulate.
// Callers must hold r.mu.
func (r *articleEditLeaseRegistry) sweepExpiredLocked(now time.Time) {
	for slug, lease := range r.leases {
		if !lease.ExpiresAt.After(now) {
			delete(r.leases, slug)
		}
	}
}

// articleEditLeases is the process-wide registry of editing leases.
var articleEditLeases = newArticleEditLeaseRegistry()
