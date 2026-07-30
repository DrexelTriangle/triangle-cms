package akismet

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckCommentPostsExpectedFieldsAndMapsSpam(t *testing.T) {
	var gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		gotUserAgent = r.Header.Get("User-Agent")
		if contentType := r.Header.Get("Content-Type"); contentType != "application/x-www-form-urlencoded" {
			t.Fatalf("expected form content type, got %q", contentType)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("failed to parse form: %v", err)
		}

		want := map[string]string{
			"api_key":              "test-key",
			"blog":                 "https://example.org",
			"user_ip":              "203.0.113.10",
			"user_agent":           "Mozilla/5.0",
			"referrer":             "https://referrer.example/search",
			"permalink":            "https://example.org/article/story",
			"comment_type":         "comment",
			"comment_author":       "akismet-guaranteed-spam",
			"comment_author_email": "spam@example.org",
			"comment_author_url":   "https://spammer.example",
			"comment_content":      "spam payload",
			"comment_date_gmt":     "2026-07-30T12:00:00Z",
			"blog_charset":         "UTF-8",
		}
		for key, value := range want {
			if got := r.Form.Get(key); got != value {
				t.Fatalf("form %s = %q, want %q", key, got, value)
			}
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("true"))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		APIKey:    "test-key",
		BlogURL:   "https://example.org",
		Endpoint:  server.URL,
		UserAgent: "TriangleCMS/test",
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	isSpam, err := client.CheckComment(t.Context(), Comment{
		UserIP:      "203.0.113.10",
		UserAgent:   "Mozilla/5.0",
		Referrer:    "https://referrer.example/search",
		Permalink:   "https://example.org/article/story",
		Author:      "akismet-guaranteed-spam",
		AuthorEmail: "spam@example.org",
		AuthorURL:   "https://spammer.example",
		Content:     "spam payload",
		CreatedAt:   time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CheckComment returned error: %v", err)
	}
	if !isSpam {
		t.Fatal("expected spam verdict")
	}
	if gotUserAgent != "TriangleCMS/test" {
		t.Fatalf("request User-Agent = %q, want %q", gotUserAgent, "TriangleCMS/test")
	}
}

func TestCheckCommentMapsHam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("false"))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		APIKey:   "test-key",
		BlogURL:  "https://example.org",
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	isSpam, err := client.CheckComment(t.Context(), Comment{UserIP: "203.0.113.10", Content: "hello"})
	if err != nil {
		t.Fatalf("CheckComment returned error: %v", err)
	}
	if isSpam {
		t.Fatal("expected ham verdict")
	}
}

func TestCheckCommentRejectsUnexpectedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid"))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		APIKey:   "test-key",
		BlogURL:  "https://example.org",
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	_, err = client.CheckComment(t.Context(), Comment{UserIP: "203.0.113.10", Content: "hello"})
	if err == nil {
		t.Fatal("expected unexpected response error")
	}
	if !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("expected unexpected response error, got %v", err)
	}
}

func TestNewClientRequiresFullBlogURL(t *testing.T) {
	_, err := NewClient(Config{APIKey: "test-key", BlogURL: "example.org"})
	if err == nil {
		t.Fatal("expected blog URL validation error")
	}
	if !strings.Contains(err.Error(), "full URL") {
		t.Fatalf("expected full URL error, got %v", err)
	}
}
