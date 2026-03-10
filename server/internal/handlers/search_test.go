package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFindStructs(t *testing.T) {
	dir := t.TempDir()

	goFile := filepath.Join(dir, "sample.go")
	content := `package sample

type Foo struct {
	Name string
}

type Bar struct {
	Value int
}

type NotAStruct int
`
	if err := os.WriteFile(goFile, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	structs, err := findStructs(dir)
	if err != nil {
		t.Fatalf("findStructs returned error: %v", err)
	}

	if len(structs) != 2 {
		t.Fatalf("expected 2 structs, got %d: %+v", len(structs), structs)
	}

	names := map[string]bool{}
	for _, s := range structs {
		names[s.Name] = true
		if s.File == "" {
			t.Errorf("struct %q has empty File field", s.Name)
		}
	}
	if !names["Foo"] {
		t.Errorf("expected struct Foo not found")
	}
	if !names["Bar"] {
		t.Errorf("expected struct Bar not found")
	}
}

func TestFindStructs_Empty(t *testing.T) {
	dir := t.TempDir()

	structs, err := findStructs(dir)
	if err != nil {
		t.Fatalf("findStructs returned error: %v", err)
	}
	if len(structs) != 0 {
		t.Fatalf("expected 0 structs, got %d", len(structs))
	}
}

func TestFindStructs_InvalidRoot(t *testing.T) {
	_, err := findStructs("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent root, got nil")
	}
}

func TestSearchStructsHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/structs", nil)
	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(SearchStructs)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", contentType)
	}

	body, _ := io.ReadAll(rr.Body)

	var payload SearchStructsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}

	if payload.Status != "OK" {
		t.Errorf("expected status OK, got %q", payload.Status)
	}
	if payload.Code != http.StatusOK {
		t.Errorf("expected code %d, got %d", http.StatusOK, payload.Code)
	}
	if payload.Structs == nil {
		t.Error("expected non-nil structs slice")
	}
}
