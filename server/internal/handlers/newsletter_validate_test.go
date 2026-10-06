package handlers

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeNewsletterEmail(t *testing.T) {
	accept := map[string]string{
		"Foo@Example.com":           "foo@example.com",
		"  a.b+tag@sub.drexel.edu ": "a.b+tag@sub.drexel.edu",
		"o'neil@drexel.edu":         "o'neil@drexel.edu",
	}
	for in, want := range accept {
		got, ok := normalizeNewsletterEmail(in)
		if !ok || got != want {
			t.Errorf("normalizeNewsletterEmail(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}

	reject := []string{
		"",
		"   ",
		"Bob <bob@x.io>",
		"<bob@x.io>",
		"bob@x.io\r\nBcc: v@x.io",
		"bob@x.io\nBcc: v@x.io",
		" bob @x.io",
		"bob@x",
		"bob@@x.io",
		"a@b@x.io",
		"bob@.x.io",
		"bob@x.io.",
		"bob@-x.io",
		"bob@x-.io",
		"bob@x..io",
		"(c)bob@x.io",
		"bob(c)@x.io",
		`"bob"@x.io`,
		strings.Repeat("a", 65) + "@x.io",
		strings.Repeat("a", 60) + "@" + strings.Repeat("b", 190) + ".io",
		"bob@x.io\x00",
		"bob@exаmple.com", // Cyrillic а in the domain
		"bób@example.com", // non-ASCII local part
		"bob@x.io,eve@x.io",
		"@x.io",
		"bob@",
	}
	for _, in := range reject {
		if got, ok := normalizeNewsletterEmail(in); ok {
			t.Errorf("normalizeNewsletterEmail(%q) accepted as %q", in, got)
		}
	}
}

func TestNormalizeNewsletterName(t *testing.T) {
	for in, want := range map[string]string{"": "", "  Ann  ": "Ann", "José Núñez": "José Núñez"} {
		got, ok := normalizeNewsletterName(in)
		if !ok || got != want {
			t.Errorf("normalizeNewsletterName(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{strings.Repeat("é", 101), "a\x07b", "\xff", "line\nbreak", "tab\tname"} {
		if got, ok := normalizeNewsletterName(in); ok {
			t.Errorf("normalizeNewsletterName(%q) accepted as %q", in, got)
		}
	}
	if _, ok := normalizeNewsletterName(strings.Repeat("é", 100)); !ok {
		t.Error("a 100-rune name was rejected; the limit is runes, not bytes")
	}
}

func TestNormalizeListIDs(t *testing.T) {
	got, ok := normalizeListIDs([]int64{3, 1, 3}, 1, 20)
	if !ok || !reflect.DeepEqual(got, []int64{1, 3}) {
		t.Errorf("dedupe/sort = %v, %v; want [1 3], true", got, ok)
	}
	for name, ids := range map[string][]int64{"empty": {}, "nil": nil, "zero": {0}, "negative": {2, -1}} {
		if _, ok := normalizeListIDs(ids, 1, 20); ok {
			t.Errorf("%s accepted", name)
		}
	}
	tooMany := make([]int64, 21)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	if _, ok := normalizeListIDs(tooMany, 1, 20); ok {
		t.Error("21 distinct lists accepted")
	}
	dupes := append(append([]int64{}, tooMany[:20]...), 1)
	if got, ok := normalizeListIDs(dupes, 1, 20); !ok || len(got) != 20 {
		t.Errorf("21 entries that dedupe to 20 = %d, %v; want accepted", len(got), ok)
	}
	if got, ok := normalizeListIDs(nil, 0, 20); !ok || got == nil || len(got) != 0 {
		t.Errorf("min 0 with no ids = %v, %v; want empty non-nil slice", got, ok)
	}
}
