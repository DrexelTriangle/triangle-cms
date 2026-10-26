package mailer

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeGoogle answers the token endpoint at /token and hands sends at /send to
// the test's handler.
func fakeGoogle(t *testing.T, send http.HandlerFunc) (*GmailSender, *atomic.Int32) {
	t.Helper()
	var tokens atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if r.PostFormValue("grant_type") != "refresh_token" || r.PostFormValue("refresh_token") != "refresh-1" ||
			r.PostFormValue("client_id") != "client" || r.PostFormValue("client_secret") != "secret" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		tokens.Add(1)
		io.WriteString(w, `{"access_token":"access-1","expires_in":3599,"token_type":"Bearer"}`)
	})
	mux.HandleFunc("POST /send", send)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &GmailSender{
		ClientID: "client", ClientSecret: "secret", RefreshToken: "refresh-1",
		TokenURL: srv.URL + "/token", SendURL: srv.URL + "/send",
	}, &tokens
}

func testMessage() *Message {
	return &Message{From: testFrom, To: testTo, Subject: "To The Point", HTML: "<p>Hi</p>", Text: "Hi",
		UnsubscribeURL: "https://cms.example.org/v1/newsletter/unsubscribe?t=abc"}
}

func TestGmailSender_UploadsTheMessageAndCachesTheToken(t *testing.T) {
	var got []string
	s, tokens := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" || r.Header.Get("Content-Type") != "message/rfc822" {
			t.Errorf("headers = %v", r.Header)
		}
		raw, _ := io.ReadAll(r.Body)
		got = append(got, string(raw))
		io.WriteString(w, `{"id":"18f","threadId":"18f","labelIds":["SENT"]}`)
	})
	for i := 0; i < 3; i++ {
		if err := s.Send(context.Background(), testMessage()); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if n := tokens.Load(); n != 1 {
		t.Errorf("token fetched %d times, want 1", n)
	}
	msg, parts := parse(t, []byte(got[0]))
	if msg.Header.Get("To") != "<reader@example.com>" || msg.Header.Get("List-Unsubscribe-Post") != "List-Unsubscribe=One-Click" {
		t.Errorf("headers = %v", msg.Header)
	}
	if parts["text/html"] != "<p>Hi</p>" {
		t.Errorf("html = %q", parts["text/html"])
	}
}

func TestGmailSender_RefetchesTheTokenAfter401(t *testing.T) {
	var sends atomic.Int32
	s, tokens := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		if sends.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"id":"1"}`)
	})
	err := s.Send(context.Background(), testMessage())
	if err == nil || IsPermanent(err) {
		t.Fatalf("first send: err = %v, want a transient error", err)
	}
	if err := s.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("second send: %v", err)
	}
	if n := tokens.Load(); n != 2 {
		t.Errorf("token fetched %d times, want 2", n)
	}
}

func TestGmailSender_ClassifiesFailuresForRetry(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		permanent bool
		reason    string
	}{
		{"bad recipient", 400, `{"error":{"code":400,"errors":[{"reason":"invalidArgument"}],"status":"INVALID_ARGUMENT"}}`, true, "invalidArgument"},
		{"rate limited", 429, `{"error":{"code":429,"errors":[{"reason":"rateLimitExceeded"}]}}`, false, "rateLimitExceeded"},
		{"daily quota", 403, `{"error":{"code":403,"errors":[{"reason":"dailyLimitExceeded"}]}}`, false, "dailyLimitExceeded"},
		{"gmail down", 503, `{"error":{"code":503,"status":"UNAVAILABLE"}}`, false, "UNAVAILABLE"},
		{"html error page", 502, `<html>bad gateway</html>`, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				io.WriteString(w, c.body)
			})
			err := s.Send(context.Background(), testMessage())
			if err == nil {
				t.Fatal("send succeeded")
			}
			if IsPermanent(err) != c.permanent {
				t.Errorf("IsPermanent = %v, want %v (%v)", IsPermanent(err), c.permanent, err)
			}
			if c.reason != "" && !strings.Contains(err.Error(), c.reason) {
				t.Errorf("error %q doesn't name %q", err, c.reason)
			}
		})
	}
}

func TestGmailSender_RevokedRefreshTokenIsTransientAndQuiet(t *testing.T) {
	s, _ := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("sent without a token")
	})
	s.RefreshToken = "revoked"
	err := s.Send(context.Background(), testMessage())
	if err == nil || IsPermanent(err) {
		t.Fatalf("err = %v, want a transient error", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "secret") {
		t.Errorf("error = %q", err)
	}
}

func TestGmailSender_DroppedAfterTheMessageWentIsPermanent(t *testing.T) {
	s, _ := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	})
	if err := s.Send(context.Background(), testMessage()); !IsPermanent(err) {
		t.Fatalf("err = %v, want permanent: the message may have gone", err)
	}

	// Refused before anything was written: nothing went, so retry.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.SendURL = "http://" + l.Addr().String() + "/send"
	l.Close()
	if err := s.Send(context.Background(), testMessage()); err == nil || IsPermanent(err) {
		t.Fatalf("err = %v, want transient", err)
	}
}

func TestGmailSender_UnbuildableMessageIsPermanent(t *testing.T) {
	s, _ := fakeGoogle(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("sent an unbuildable message")
	})
	m := testMessage()
	m.HTML = ""
	if err := s.Send(context.Background(), m); !IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}
