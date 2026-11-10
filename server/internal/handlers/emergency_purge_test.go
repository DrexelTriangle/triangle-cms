package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"server/internal/cdnpurge"
	"server/internal/middleware"
	"server/internal/models"
)

type hostPurgeServer struct {
	mu     sync.Mutex
	hosts  [][]string
	status int
}

func newHostPurgeServer(t *testing.T) (*hostPurgeServer, *cdnpurge.Purger) {
	t.Helper()
	s := &hostPurgeServer{status: http.StatusOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Hosts []string `json:"hosts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		s.mu.Lock()
		s.hosts = append(s.hosts, payload.Hosts)
		status := s.status
		s.mu.Unlock()
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"success":true}`))
		} else {
			_, _ = w.Write([]byte(`{"success":false}`))
		}
	}))
	t.Cleanup(srv.Close)
	p, err := cdnpurge.New(cdnpurge.Config{ZoneID: "z", APIToken: "t", SiteURL: "https://www.thetriangle.org", Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	SetCDNPurger(p)
	t.Cleanup(func() { SetCDNPurger(nil) })
	resetEmergencyPurgeCooldown(t)
	return s, p
}

func resetEmergencyPurgeCooldown(t *testing.T) {
	t.Helper()
	emergencyPurgeMu.Lock()
	lastEmergencyPurge = time.Time{}
	emergencyPurgeMu.Unlock()
	t.Cleanup(func() {
		emergencyPurgeMu.Lock()
		lastEmergencyPurge = time.Time{}
		emergencyPurgeMu.Unlock()
	})
}

func emergencyPurge(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/settings/cache/purge", nil)
	req = req.WithContext(middleware.ContextWithUser(context.Background(), &models.User{ID: 1, Name: "Admin", Role: models.RoleAdmin}))
	rec := httptest.NewRecorder()
	PostEmergencyCachePurge().ServeHTTP(rec, req)
	return rec
}

func TestEmergencyPurge_UnconfiguredIs503(t *testing.T) {
	SetCDNPurger(nil)
	resetEmergencyPurgeCooldown(t)
	if rec := emergencyPurge(t); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when purging is not configured", rec.Code)
	}
}

func TestEmergencyPurge_PurgesTheWholeHost(t *testing.T) {
	cf, _ := newHostPurgeServer(t)

	rec := emergencyPurge(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body models.CachePurgeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Host != "www.thetriangle.org" || body.PurgedAt == "" {
		t.Fatalf("body = %+v (%v)", body, err)
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	if len(cf.hosts) != 1 || len(cf.hosts[0]) != 1 || cf.hosts[0][0] != "www.thetriangle.org" {
		t.Fatalf("Cloudflare got %v, want one hosts purge of www.thetriangle.org", cf.hosts)
	}
}

func TestEmergencyPurge_SecondClickWithinAMinuteIs429(t *testing.T) {
	cf, _ := newHostPurgeServer(t)
	if rec := emergencyPurge(t); rec.Code != http.StatusOK {
		t.Fatalf("first purge = %d", rec.Code)
	}
	if rec := emergencyPurge(t); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second purge = %d, want 429", rec.Code)
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	if len(cf.hosts) != 1 {
		t.Fatalf("Cloudflare called %d times, want 1", len(cf.hosts))
	}
}

// A refused purge evicted nothing, so it must not start the cooldown: the
// admin has to be able to retry at once.
func TestEmergencyPurge_CloudflareErrorIs502AndAllowsRetry(t *testing.T) {
	cf, _ := newHostPurgeServer(t)
	cf.status = http.StatusForbidden
	if rec := emergencyPurge(t); rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	cf.mu.Lock()
	cf.status = http.StatusOK
	cf.mu.Unlock()
	if rec := emergencyPurge(t); rec.Code != http.StatusOK {
		t.Fatalf("retry after a failed purge = %d, want 200", rec.Code)
	}
}
