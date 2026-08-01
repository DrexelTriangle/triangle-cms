package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

const testSigningSecret = "8f742231b10e8888abcd99yyyzzz85a5"

func signSlackBody(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	io.WriteString(mac, "v0:"+timestamp+":"+body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySlackRequest_AcceptsAValidSignature(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := "1700000000"
	body := "payload=%7B%22type%22%3A%22block_actions%22%7D"

	if !verifySlackRequest(testSigningSecret, ts, body, signSlackBody(testSigningSecret, ts, body), now) {
		t.Fatal("expected a correctly signed request to verify")
	}
}

func TestVerifySlackRequest_RejectsTamperedBody(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := "1700000000"
	signature := signSlackBody(testSigningSecret, ts, "payload=original")

	if verifySlackRequest(testSigningSecret, ts, "payload=tampered", signature, now) {
		t.Fatal("expected a body that does not match the signature to be rejected")
	}
}

func TestVerifySlackRequest_RejectsWrongSecret(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := "1700000000"
	body := "payload=x"

	if verifySlackRequest(testSigningSecret, ts, body, signSlackBody("not-the-secret", ts, body), now) {
		t.Fatal("expected a signature from another secret to be rejected")
	}
}

// A captured request must not stay valid forever, in either time direction.
func TestVerifySlackRequest_RejectsStaleAndFutureTimestamps(t *testing.T) {
	body := "payload=x"
	for name, skew := range map[string]time.Duration{
		"stale":  -10 * time.Minute,
		"future": 10 * time.Minute,
	} {
		t.Run(name, func(t *testing.T) {
			signedAt := time.Unix(1700000000, 0)
			ts := "1700000000"
			now := signedAt.Add(-skew)

			if verifySlackRequest(testSigningSecret, ts, body, signSlackBody(testSigningSecret, ts, body), now) {
				t.Fatalf("expected a %s timestamp to be rejected", name)
			}
		})
	}
}

func TestVerifySlackRequest_RejectsMissingParts(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cases := map[string][4]string{
		"no secret":     {"", "1700000000", "b", "v0=abc"},
		"no timestamp":  {testSigningSecret, "", "b", "v0=abc"},
		"no signature":  {testSigningSecret, "1700000000", "b", ""},
		"bad timestamp": {testSigningSecret, "not-a-number", "b", "v0=abc"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if verifySlackRequest(c[0], c[1], c[2], c[3], now) {
				t.Fatal("expected verification to fail")
			}
		})
	}
}

// Without a signing secret nothing can be verified, so the endpoint must refuse
// rather than fall open.
func TestPostSlackClassifiedAction_RefusesWhenUnconfigured(t *testing.T) {
	t.Setenv("SLACK_SIGNING_SECRET", "")

	req := httptest.NewRequest("POST", "/v1/integrations/slack/classifieds", nil)
	rec := httptest.NewRecorder()
	PostSlackClassifiedAction(nil).ServeHTTP(rec, req)

	if rec.Code != 503 {
		t.Fatalf("expected 503 when the signing secret is unset, got %d", rec.Code)
	}
}

// An unsigned request must be turned away before the body is parsed or the
// database is touched — the nil *sql.DB here would panic if it were not.
func TestPostSlackClassifiedAction_RejectsUnsignedRequest(t *testing.T) {
	t.Setenv("SLACK_SIGNING_SECRET", testSigningSecret)

	req := httptest.NewRequest("POST", "/v1/integrations/slack/classifieds", nil)
	rec := httptest.NewRecorder()
	PostSlackClassifiedAction(nil).ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("expected 401 for an unsigned request, got %d", rec.Code)
	}
}
