package handlers

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Click-tracking links in the newsletters the WordPress Newsletter plugin sent
// (9.x). Every link in every issue was rewritten to
//
//	https://cms.thetriangle.org/wp-admin/admin-ajax.php?action=tnptr&nltr=<b64>
//
// (or /?nltr=<b64>), where <b64> is base64("email;user;url;anchor;signature")
// and signature = md5("email;user;url;anchor" + the plugin's key). Delta's
// nginx rewrites both forms here once cms.thetriangle.org points at Delta, so
// the links in years of sent issues keep reaching their articles and sponsors.
//
// The signature is what stops this from being an open redirect: anyone can
// base64 a URL. Like the plugin, a link is followed only if it verifies, or
// if it points at our own site, where an unsigned redirect harms no one. The
// plugin's key comes from NEWSLETTER_LEGACY_LINK_KEY (wp option pluck
// newsletter_main key). Without it, links to our own domains still work and
// everything else lands on the homepage. Clicks are not recorded: the stats
// they fed died with WordPress.

const legacyNewsletterLinkKeyEnv = "NEWSLETTER_LEGACY_LINK_KEY"

// legacyNewsletterFallback is where a link goes when it cannot be followed.
// Better than the plugin's "Invalid link" page for a reader with an old email.
const legacyNewsletterFallback = "https://www.thetriangle.org/"

// legacyNewsletterOwnHosts can be redirected to without a signature. The
// plugin allowed only its own host (cms.thetriangle.org); the public site's
// names are ours too.
var legacyNewsletterOwnHosts = map[string]bool{
	"cms.thetriangle.org": true,
	"www.thetriangle.org": true,
	"thetriangle.org":     true,
}

// @Summary Follow a click-tracking link from a WordPress-era newsletter
// @Description Decodes the Newsletter plugin's nltr parameter and redirects to the original link when its signature verifies (or it points at The Triangle's own site); otherwise redirects to the homepage.
// @Tags newsletter
// @Param nltr query string true "The plugin's base64 tracking payload"
// @Success 302
// @Router /v1/legacy-redirects/newsletter-click [get]
func GetLegacyNewsletterClick() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(os.Getenv(legacyNewsletterLinkKeyEnv))
		target := legacyNewsletterTarget(r.URL.Query().Get("nltr"), key)
		// Each link always resolves the same way, but a 301 would let a
		// browser keep the homepage fallback after the key is configured.
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusFound)
	})
}

// legacyNewsletterTarget returns where a tracking payload should send the
// reader: the original URL if it may be followed, the homepage otherwise.
func legacyNewsletterTarget(nltr, key string) string {
	raw, ok := decodePHPBase64(nltr)
	if !ok {
		return legacyNewsletterFallback
	}

	// The plugin's own parse: the first two fields are ids, the last two are
	// the signature and the (since emptied) anchor, and whatever is left is
	// the URL, rejoined because a URL may itself contain ';'.
	parts := strings.Split(raw, ";")
	if len(parts) < 5 {
		return legacyNewsletterFallback
	}
	emailID, userID := phpIntval(parts[0]), phpIntval(parts[1])
	signature, anchor := parts[len(parts)-1], parts[len(parts)-2]
	link := strings.Join(parts[2:len(parts)-2], ";")

	parsed, err := url.Parse(link)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return legacyNewsletterFallback
	}

	// Unsigned, so trust nothing a browser might read differently from Go:
	// no userinfo ("our.host@elsewhere" tricks), and the re-serialized URL
	// rather than the raw string.
	if legacyNewsletterOwnHosts[strings.ToLower(parsed.Hostname())] && parsed.User == nil {
		return parsed.String()
	}
	if key == "" {
		return legacyNewsletterFallback
	}
	sum := md5.Sum([]byte(strconv.FormatInt(emailID, 10) + ";" + strconv.FormatInt(userID, 10) + ";" + link + ";" + anchor + key))
	want := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(signature)), []byte(want)) != 1 {
		return legacyNewsletterFallback
	}
	return link
}

// decodePHPBase64 decodes the way PHP's base64_decode does in its default,
// non-strict mode: characters outside the alphabet are skipped and missing
// padding is tolerated, since mail clients mangle long links in both ways.
func decodePHPBase64(s string) (string, bool) {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			b.WriteRune(c)
		}
	}
	clean := b.String()
	if clean == "" {
		return "", false
	}
	out, err := base64.RawStdEncoding.DecodeString(clean)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// phpIntval mirrors PHP's (int) cast on a string: leading whitespace, an
// optional sign and the leading digits; anything else is 0. The signature was
// computed over the cast values, so this has to agree with PHP exactly.
func phpIntval(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	end := 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	digits := end
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == digits {
		return 0
	}
	n, err := strconv.ParseInt(s[:end], 10, 64)
	if err != nil {
		return 0
	}
	return n
}
