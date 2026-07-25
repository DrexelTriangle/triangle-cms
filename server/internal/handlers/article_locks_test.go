package handlers

import (
	"testing"
	"time"
)

func TestArticleEditLeaseAcquireAndRelease(t *testing.T) {
	r := newArticleEditLeaseRegistry()

	// First holder gets the lease.
	if _, granted := r.Acquire("slug-a", 1, "Alice"); !granted {
		t.Fatal("first acquire should be granted")
	}

	// A different user is blocked and sees the current holder.
	lease, granted := r.Acquire("slug-a", 2, "Bob")
	if granted {
		t.Fatal("second holder should be blocked")
	}
	if lease.HolderID != 1 || lease.HolderName != "Alice" {
		t.Fatalf("expected Alice to hold the lease, got id=%d name=%q", lease.HolderID, lease.HolderName)
	}

	// The holder can refresh their own lease.
	if _, granted := r.Acquire("slug-a", 1, "Alice"); !granted {
		t.Fatal("holder should be able to refresh")
	}

	// A stale release from a non-holder must not free the lease.
	r.Release("slug-a", 2)
	if _, granted := r.Acquire("slug-a", 3, "Carol"); granted {
		t.Fatal("non-holder release should not have freed the lease")
	}

	// The real holder releasing frees it for the next editor.
	r.Release("slug-a", 1)
	if _, granted := r.Acquire("slug-a", 3, "Carol"); !granted {
		t.Fatal("lease should be free after holder released")
	}
}

func TestArticleEditLeaseExpiry(t *testing.T) {
	r := newArticleEditLeaseRegistry()

	// Seed an already-expired lease for another holder.
	r.leases["slug-b"] = articleEditLease{HolderID: 9, HolderName: "Old", ExpiresAt: time.Now().Add(-time.Minute)}

	if _, granted := r.Acquire("slug-b", 5, "New"); !granted {
		t.Fatal("expired lease should be treated as free")
	}
}
