package newsletter

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeLookup struct {
	slugs     map[string]int64
	articles  map[int64]ArticleRef
	slugCalls [][]string
	byIDCalls [][]int64
}

func (f *fakeLookup) ArticleIDsBySlug(_ context.Context, slugs []string) (map[string]int64, error) {
	f.slugCalls = append(f.slugCalls, append([]string(nil), slugs...))
	out := map[string]int64{}
	for _, s := range slugs {
		if id, ok := f.slugs[s]; ok {
			out[s] = id
		}
	}
	return out, nil
}

func (f *fakeLookup) ArticlesByID(_ context.Context, ids []int64) (map[int64]ArticleRef, error) {
	f.byIDCalls = append(f.byIDCalls, append([]int64(nil), ids...))
	out := map[int64]ArticleRef{}
	for _, id := range ids {
		if a, ok := f.articles[id]; ok {
			out[id] = a
		}
	}
	return out, nil
}

func lookupFixture() *fakeLookup {
	return &fakeLookup{
		slugs: map[string]int64{"denim-day": 11, "mob": 12},
		articles: map[int64]ArticleRef{
			11: {ID: 11, Slug: "denim-day", Title: "Denim Day", Published: true},
			12: {ID: 12, Slug: "mob", Title: "The mob", Published: true},
		},
	}
}

func textDoc(html string) Document {
	return Document{Version: 1, Blocks: []Block{{Type: BlockText, HTML: html}}}
}

func TestNormalize_LocksArticleLinks(t *testing.T) {
	lk := lookupFixture()
	in := textDoc(`<p><a href="https://www.thetriangle.org/article/denim-day?utm=x#top">one</a> ` +
		`<a href="https://thetriangle.org/article/mob">two</a> ` +
		`<a href="/article/denim-day/">three</a> ` +
		`<a href="https://example.com/article/denim-day">elsewhere</a></p>`)
	in.Blocks = append(in.Blocks, Block{Type: BlockButton, Label: "Read", Href: "https://www.thetriangle.org/article/mob"})

	out, err := Normalize(context.Background(), in, lk, "https://www.thetriangle.org")
	if err != nil {
		t.Fatal(err)
	}
	html := out.Blocks[0].HTML
	for _, want := range []string{`<a href="cms-article:11">one</a>`, `<a href="cms-article:12">two</a>`, `<a href="cms-article:11">three</a>`, `<a href="https://example.com/article/denim-day">elsewhere</a>`} {
		if !strings.Contains(html, want) {
			t.Errorf("normalized html missing %s:\n%s", want, html)
		}
	}
	if strings.Contains(html, "thetriangle.org") {
		t.Errorf("a public article URL survived normalization:\n%s", html)
	}
	if out.Blocks[1].Href != "cms-article:12" {
		t.Errorf("button href = %q, want cms-article:12", out.Blocks[1].Href)
	}
	if len(lk.slugCalls) != 1 {
		t.Errorf("slug lookups = %d, want one batched call", len(lk.slugCalls))
	}
}

func TestNormalize_LocksConfiguredSiteHost(t *testing.T) {
	out, err := Normalize(context.Background(), textDoc(`<a href="https://dev.thetriangle.org/article/mob">x</a>`), lookupFixture(), "https://dev.thetriangle.org/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Blocks[0].HTML, `href="cms-article:12"`) {
		t.Errorf("dev-site link not locked: %s", out.Blocks[0].HTML)
	}
}

func TestNormalize_UnknownSlugIsValidationError(t *testing.T) {
	_, err := Normalize(context.Background(), textDoc(`<a href="https://www.thetriangle.org/article/nope">x</a>`), lookupFixture(), "https://www.thetriangle.org")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want *ValidationError", err)
	}
	if !strings.Contains(ve.Error(), "https://www.thetriangle.org/article/nope") {
		t.Errorf("message %q does not name the link", ve.Error())
	}
}

func TestNormalize_UnknownArticleBlockID(t *testing.T) {
	doc := Document{Version: 1, Blocks: []Block{{Type: BlockArticle, ArticleID: 999}}}
	_, err := Normalize(context.Background(), doc, lookupFixture(), "https://www.thetriangle.org")
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Error(), "999") {
		t.Fatalf("err = %v, want *ValidationError naming 999", err)
	}
}

func TestNormalize_ArticleBlockDefaultsAndStripsForeignFields(t *testing.T) {
	doc := Document{Version: 1, Blocks: []Block{{Type: BlockArticle, ArticleID: 11, Text: "stray", Href: "https://x.io"}}}
	out, err := Normalize(context.Background(), doc, lookupFixture(), "https://www.thetriangle.org")
	if err != nil {
		t.Fatal(err)
	}
	b := out.Blocks[0]
	if b.ShowImage == nil || !*b.ShowImage || b.ShowExcerpt == nil || !*b.ShowExcerpt {
		t.Errorf("article block flags not defaulted to true: %+v", b)
	}
	if b.Text != "" || b.Href != "" {
		t.Errorf("fields that do not belong to an article block survived: %+v", b)
	}
}

func TestNormalize_Idempotent(t *testing.T) {
	in := textDoc(`<p onclick="x">Hi <a href="https://www.thetriangle.org/article/mob">mob</a><script>bad()</script></p>`)
	once, err := Normalize(context.Background(), in, lookupFixture(), "https://www.thetriangle.org")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Normalize(context.Background(), once, lookupFixture(), "https://www.thetriangle.org")
	if err != nil {
		t.Fatal(err)
	}
	if once.Blocks[0].HTML != twice.Blocks[0].HTML {
		t.Errorf("not idempotent:\n once: %s\ntwice: %s", once.Blocks[0].HTML, twice.Blocks[0].HTML)
	}
}

func TestNormalize_KeepsExistingLockedLinks(t *testing.T) {
	lk := lookupFixture()
	out, err := Normalize(context.Background(), textDoc(`<a href="cms-article:11">x</a>`), lk, "https://www.thetriangle.org")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Blocks[0].HTML, `href="cms-article:11"`) {
		t.Errorf("locked link lost: %s", out.Blocks[0].HTML)
	}
	for _, call := range lk.slugCalls {
		if len(call) > 0 {
			t.Errorf("slug lookup made for an already-locked link: %v", call)
		}
	}
}

func TestArticleIDFromHref(t *testing.T) {
	cases := map[string]int64{"cms-article:11": 11, "cms-article:0": 0, "cms-article:x": 0, "https://x.io": 0, "cms-article:-3": 0}
	for in, want := range cases {
		got, ok := ArticleIDFromHref(in)
		if got != want || ok != (want > 0) {
			t.Errorf("ArticleIDFromHref(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
}
