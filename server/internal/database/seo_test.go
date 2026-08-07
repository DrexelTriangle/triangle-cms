package database

import (
	"strings"
	"testing"
)

func TestNormalizeCanonicalURLAcceptsBlankAsNoOverride(t *testing.T) {
	// Blank is the common case -- the public site falls back to its own
	// /article/<slug> URL -- so it must validate rather than 400 every save.
	for _, raw := range []string{"", "   ", "\t\n"} {
		got, ok := NormalizeCanonicalURL(raw)
		if !ok {
			t.Errorf("NormalizeCanonicalURL(%q) rejected a blank override", raw)
		}
		if got != "" {
			t.Errorf("NormalizeCanonicalURL(%q) = %q, want empty", raw, got)
		}
	}
}

func TestNormalizeCanonicalURLAcceptsAbsoluteHTTPURLs(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/story",
		"http://example.com/story?utm=1",
		"  https://www.thetriangle.org/article/foo  ",
	} {
		got, ok := NormalizeCanonicalURL(raw)
		if !ok {
			t.Errorf("NormalizeCanonicalURL(%q) rejected a valid absolute URL", raw)
		}
		if got != strings.TrimSpace(raw) {
			t.Errorf("NormalizeCanonicalURL(%q) = %q, want the trimmed input", raw, got)
		}
	}
}

func TestNormalizeCanonicalURLRejectsNonAbsoluteURLs(t *testing.T) {
	// A relative or scheme-less canonical is worse than none: the browser
	// resolves it against whatever origin served the page.
	for _, raw := range []string{
		"/article/foo",
		"example.com/story",
		"ftp://example.com/story",
		"javascript:alert(1)",
		"https://",
	} {
		if _, ok := NormalizeCanonicalURL(raw); ok {
			t.Errorf("NormalizeCanonicalURL(%q) accepted a non-absolute URL", raw)
		}
	}
}

// issueMessages flattens an audit result so tests can assert on what was raised.
func issueMessages(t *testing.T, photoURL, excerpt, metaDesc string) []string {
	t.Helper()
	issues := auditArticle(1, "some-slug", "Some Title", "An SEO Title", metaDesc, "keyword", photoURL, excerpt)
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		messages = append(messages, issue.Issue)
	}
	return messages
}

func containsSubstring(messages []string, want string) bool {
	for _, message := range messages {
		if strings.Contains(message, want) {
			return true
		}
	}
	return false
}

func TestAuditArticleFlagsMissingFeaturedImage(t *testing.T) {
	longEnoughDesc := strings.Repeat("a", 100)

	// "-1" is the sentinel WordPress-imported rows carry instead of NULL when
	// the post had no thumbnail, so it has to count as missing.
	for _, photoURL := range []string{"", "   ", "-1"} {
		messages := issueMessages(t, photoURL, "An excerpt", longEnoughDesc)
		if !containsSubstring(messages, "Missing featured image") {
			t.Errorf("photo_url %q: expected a missing-featured-image issue, got %v", photoURL, messages)
		}
	}

	messages := issueMessages(t, "https://cms.thetriangle.org/wp-content/uploads/a.jpg", "An excerpt", longEnoughDesc)
	if containsSubstring(messages, "Missing featured image") {
		t.Errorf("a real photo_url should not be flagged, got %v", messages)
	}
}

func TestAuditArticleFallsBackToTitleForSEOTitle(t *testing.T) {
	longEnoughDesc := strings.Repeat("a", 100)
	photoURL := "https://example.com/a.jpg"

	// The public site renders the article title when seo_title is blank, so a
	// blank column with a usable title is not an issue worth reporting.
	issues := auditArticle(1, "some-slug", "A short headline", "", longEnoughDesc, "keyword", photoURL, "An excerpt")
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		messages = append(messages, issue.Issue)
	}
	if containsSubstring(messages, "Missing SEO title") {
		t.Errorf("a blank seo_title with a title to fall back on should not be flagged, got %v", messages)
	}

	// The length check has to follow the fallback, because an over-long
	// headline is what gets truncated in search results.
	longTitle := strings.Repeat("a", seoTitleMaxLen+1)
	issues = auditArticle(1, "some-slug", longTitle, "", longEnoughDesc, "keyword", photoURL, "An excerpt")
	messages = messages[:0]
	for _, issue := range issues {
		messages = append(messages, issue.Issue)
	}
	if !containsSubstring(messages, "SEO title exceeds") {
		t.Errorf("an over-long fallback title should be flagged, got %v", messages)
	}

	// With neither, the page has no title at all.
	issues = auditArticle(1, "some-slug", "   ", "", longEnoughDesc, "keyword", photoURL, "An excerpt")
	messages = messages[:0]
	for _, issue := range issues {
		messages = append(messages, issue.Issue)
	}
	if !containsSubstring(messages, "Missing SEO title") {
		t.Errorf("expected a missing-SEO-title issue when there is no title either, got %v", messages)
	}
}

func TestAuditArticleFlagsNoDescriptionAndNoExcerpt(t *testing.T) {
	// The public site falls back to the excerpt when meta_description is blank,
	// so only the combination leaves the page with no description at all.
	messages := issueMessages(t, "https://example.com/a.jpg", "", "")
	if !containsSubstring(messages, "no excerpt to fall back on") {
		t.Errorf("expected a no-description-and-no-excerpt issue, got %v", messages)
	}

	messages = issueMessages(t, "https://example.com/a.jpg", "An excerpt saves it", "")
	if containsSubstring(messages, "no excerpt to fall back on") {
		t.Errorf("an excerpt should satisfy the fallback, got %v", messages)
	}
}
