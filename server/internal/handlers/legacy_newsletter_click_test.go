package handlers

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const testLinkKey = "0123456789abcdef0123456789abcdef"

// trackerPayload builds nltr exactly as the plugin's relink_callback does:
// "email;user;href;anchor" + ";" + md5(that + key), base64'd, rawurlencoded.
func trackerPayload(emailID, userID, href, key string) string {
	r := emailID + ";" + userID + ";" + href + ";"
	sum := md5.Sum([]byte(r + key))
	r += ";" + hex.EncodeToString(sum[:])
	return url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(r)))
}

func TestLegacyNewsletterTarget(t *testing.T) {
	sponsor := "https://gemini.google/?utm_source=thetriangle&utm_medium=email"
	article := "https://www.thetriangle.org/article/some-story"
	withSemicolon := "https://example.com/a;b?c=1"

	unescape := func(s string) string {
		v, err := url.QueryUnescape(s)
		if err != nil {
			t.Fatalf("unescape: %v", err)
		}
		return v
	}

	cases := []struct {
		name, nltr, key, want string
	}{
		{"signed external link", trackerPayload("52", "1234", sponsor, testLinkKey), testLinkKey, sponsor},
		{"url containing a semicolon", trackerPayload("52", "1234", withSemicolon, testLinkKey), testLinkKey, withSemicolon},
		{"own site needs no signature", trackerPayload("52", "1234", article, "some-other-key"), "", article},
		{"legacy host needs no signature", trackerPayload("52", "1234", "https://cms.thetriangle.org/news/x/", "x"), "", "https://cms.thetriangle.org/news/x/"},
		{"wrong key is not an open redirect", trackerPayload("52", "1234", sponsor, "attacker"), testLinkKey, legacyNewsletterFallback},
		{"no key configured", trackerPayload("52", "1234", sponsor, testLinkKey), "", legacyNewsletterFallback},
		{"test email ids of 0 still verify", trackerPayload("0", "0", sponsor, testLinkKey), testLinkKey, sponsor},
		{"garbage", "!!!", testLinkKey, legacyNewsletterFallback},
		{"too few fields", url.QueryEscape(base64.StdEncoding.EncodeToString([]byte("1;2;https://x.example/"))), testLinkKey, legacyNewsletterFallback},
		{"javascript scheme", trackerPayload("1", "2", "javascript:alert(1)", testLinkKey), testLinkKey, legacyNewsletterFallback},
		{"protocol-relative", trackerPayload("1", "2", "//evil.example/", testLinkKey), testLinkKey, legacyNewsletterFallback},
		{"own host as userinfo", trackerPayload("1", "2", "https://www.thetriangle.org@evil.example/", "x"), "", legacyNewsletterFallback},
		{"userinfo on own host", trackerPayload("1", "2", "https://evil.example@www.thetriangle.org/", "x"), "", legacyNewsletterFallback},
		{"backslash authority", trackerPayload("1", "2", `https://evil.example\@www.thetriangle.org/`, "x"), "", legacyNewsletterFallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := legacyNewsletterTarget(unescape(tc.nltr), tc.key); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Mail clients drop base64 padding and sometimes break long links; PHP's
// lenient decoder shrugs both off, so this must too.
func TestLegacyNewsletterTarget_ToleratesMangledBase64(t *testing.T) {
	sponsor := "https://vote.pa.gov/"
	payload := unescapeOrFail(t, trackerPayload("52", "1234", sponsor, testLinkKey))
	mangled := ""
	for i, c := range payload {
		if c == '=' {
			continue
		}
		if i == 10 {
			mangled += " \n"
		}
		mangled += string(c)
	}
	if got := legacyNewsletterTarget(mangled, testLinkKey); got != sponsor {
		t.Errorf("got %q, want %q", got, sponsor)
	}
}

func TestPHPIntval(t *testing.T) {
	for in, want := range map[string]int64{"52": 52, " 7x": 7, "-3": -3, "abc": 0, "": 0, "+": 0} {
		if got := phpIntval(in); got != want {
			t.Errorf("phpIntval(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestGetLegacyNewsletterClick_Redirects(t *testing.T) {
	t.Setenv(legacyNewsletterLinkKeyEnv, testLinkKey)
	sponsor := "https://gemini.google/"
	req := httptest.NewRequest(http.MethodGet, "/v1/legacy-redirects/newsletter-click?nltr="+trackerPayload("52", "1234", sponsor, testLinkKey), nil)
	rec := httptest.NewRecorder()

	GetLegacyNewsletterClick().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != sponsor {
		t.Errorf("Location = %q, want %q", loc, sponsor)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func unescapeOrFail(t *testing.T, s string) string {
	t.Helper()
	v, err := url.QueryUnescape(s)
	if err != nil {
		t.Fatalf("unescape: %v", err)
	}
	return v
}
