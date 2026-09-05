package activity

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"server/internal/middleware"
	"server/internal/models"
)

type recordingStore struct {
	entries    []Entry
	err        error
	contextErr error
}

func (s *recordingStore) Write(ctx context.Context, entry Entry) error {
	s.contextErr = ctx.Err()
	s.entries = append(s.entries, entry)
	return s.err
}

func (s *recordingStore) List(context.Context, Query) (ListResult, error) {
	return ListResult{Entries: s.entries, TotalCount: len(s.entries)}, nil
}

func setupLogTest(t *testing.T, store Store) *bytes.Buffer {
	t.Helper()
	previousStore, previousLogger := DefaultStore(), slog.Default()
	var output bytes.Buffer
	SetDefaultStore(store)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		SetDefaultStore(previousStore)
		slog.SetDefault(previousLogger)
	})
	return &output
}

func TestLogRequestPersistsAuditAndLogsToStdout(t *testing.T) {
	store := &recordingStore{}
	output := setupLogTest(t, store)
	r := httptest.NewRequest("PATCH", "/v1/articles/42", nil)
	ctx, cancel := context.WithCancel(middleware.ContextWithUser(r.Context(), &models.User{
		ID: 7, Name: "Editor", Role: models.RoleEditor,
	}))
	cancel()
	LogRequest(r.WithContext(ctx), " article_updated ", " Headline ", "article_id", 42, "status", 200)
	if len(store.entries) != 1 {
		t.Fatalf("entries = %d", len(store.entries))
	}
	e := store.entries[0]
	if e.Action != "article_updated" || e.Target != "Headline" || e.Kind != "activity" || e.Level != "INFO" {
		t.Fatalf("unexpected event: %+v", e)
	}
	if e.ActorID != 7 || e.ActorName != "Editor" || e.ActorRole != "editor" || e.Method != "PATCH" || e.Path != "/v1/articles/42" || e.Status != 200 {
		t.Fatalf("unexpected request metadata: %+v", e)
	}
	if e.Attributes["article_id"] != int64(42) || e.Attributes["service"] != "cms" || e.Timestamp.IsZero() {
		t.Fatalf("unexpected attributes or timestamp: %+v", e)
	}
	if store.contextErr != nil {
		t.Fatalf("audit inherited cancelled request: %v", store.contextErr)
	}
	if !strings.Contains(output.String(), `"msg":"activity event"`) {
		t.Fatal(output.String())
	}
	slog.Info("ordinary application log")
	if len(store.entries) != 1 {
		t.Fatal("ordinary logs must not be persisted")
	}
}

func TestLogRequestReportsStoreFailureWithoutRecursion(t *testing.T) {
	store := &recordingStore{err: errors.New("database unavailable")}
	output := setupLogTest(t, store)
	LogRequest(nil, "settings_changed", "Site title")
	if len(store.entries) != 1 || store.entries[0].ActorID != 0 {
		t.Fatalf("entries: %+v", store.entries)
	}
	if !strings.Contains(output.String(), "failed to persist activity") {
		t.Fatal(output.String())
	}
}

func TestLogRequestWithoutStoreStillLogs(t *testing.T) {
	output := setupLogTest(t, nil)
	LogRequest(nil, "settings_changed", "Site title")
	if !strings.Contains(output.String(), "activity event") {
		t.Fatal(output.String())
	}
}

func TestLogRequestPreservesMediaPathAttribute(t *testing.T) {
	store := &recordingStore{}
	setupLogTest(t, store)
	LogRequest(httptest.NewRequest("POST", "/v1/media", nil), "media_uploaded", "photo.jpg", "path", "2026/09/photo.jpg")
	if store.entries[0].Path != "2026/09/photo.jpg" {
		t.Fatalf("path = %q", store.entries[0].Path)
	}
}
