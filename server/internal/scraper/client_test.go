package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDisabledClient(t *testing.T) {
	if _, err := New("", time.Second).InstagramImages(context.Background(), "https://www.instagram.com/p/abc/"); err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}

func TestInstagramImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instagram" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["url"] != "https://www.instagram.com/p/abc/" {
			t.Errorf("bad body: %v %v", body, err)
		}
		json.NewEncoder(w).Encode(map[string][]string{"images": {"https://cdn.test/1.jpg", "https://cdn.test/2.jpg"}})
	}))
	defer srv.Close()

	got, err := New(srv.URL, time.Second).InstagramImages(context.Background(), "https://www.instagram.com/p/abc/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "https://cdn.test/1.jpg" {
		t.Fatalf("got %v", got)
	}
}

func TestInstagramImagesSidecarError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"could not load the post"}`, http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, time.Second).InstagramImages(context.Background(), "https://www.instagram.com/p/abc/"); err == nil {
		t.Fatal("want an error for a 502")
	}
}
