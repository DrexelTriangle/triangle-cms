package newsletter

import (
	"context"
	"strings"
	"testing"
)

func renderFixture() *fakeLookup {
	return &fakeLookup{
		articles: map[int64]ArticleRef{
			11: {ID: 11, Slug: "denim-day-renamed", Title: "Denim Day", Excerpt: "<p>Every year, <b>Denim Day</b> raises awareness.</p>", ImageURL: "https://cdn.example/denim.jpg", ImageAlt: "Students in denim", Published: true},
			12: {ID: 12, Slug: "draft-piece", Title: "Draft piece", Published: false},
		},
	}
}

var previewOpts = RenderOptions{SiteURL: "https://www.thetriangle.org", Subject: "Weekly", UnsubscribeURL: "#", ManageURL: "#", ViewOnlineURL: "#"}

func render(t *testing.T, blocks ...Block) Rendered {
	t.Helper()
	out, err := Render(context.Background(), Document{Version: 1, Blocks: blocks}, renderFixture(), previewOpts)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return out
}

func TestRender_ResolvesLockedLinksToCurrentSlug(t *testing.T) {
	out := render(t,
		Block{Type: BlockText, HTML: `<p>Read <a href="cms-article:11">this</a>.</p>`},
		Block{Type: BlockButton, Label: "Go", Href: "cms-article:11"},
	)
	if strings.Count(out.HTML, `href="https://www.thetriangle.org/article/denim-day-renamed"`) < 2 {
		t.Errorf("locked links did not resolve to the live URL:\n%s", out.HTML)
	}
	if strings.Contains(out.HTML, "cms-article:") {
		t.Error("an unresolved cms-article: link reached the email")
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", out.Warnings)
	}
}

func TestRender_ArticleBlockUsesLiveData(t *testing.T) {
	yes, no := true, false
	out := render(t, Block{Type: BlockArticle, ArticleID: 11, ShowImage: &yes, ShowExcerpt: &yes})
	for _, want := range []string{"Denim Day", "Every year, Denim Day raises awareness.", `src="https://cdn.example/denim.jpg"`, `alt="Students in denim"`, `href="https://www.thetriangle.org/article/denim-day-renamed"`} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("article block missing %q", want)
		}
	}
	if strings.Contains(out.HTML, "<b>Denim Day</b>") {
		t.Error("excerpt markup was passed through; it must render as plain text")
	}

	bare := render(t, Block{Type: BlockArticle, ArticleID: 11, ShowImage: &no, ShowExcerpt: &no})
	if strings.Contains(bare.HTML, "cdn.example/denim.jpg") || strings.Contains(bare.HTML, "raises awareness") {
		t.Error("show_image/show_excerpt false still rendered the image or excerpt")
	}
	if !strings.Contains(bare.HTML, "Denim Day") {
		t.Error("article title missing when image and excerpt are hidden")
	}
}

func TestRender_UnpublishedAndMissingWarn(t *testing.T) {
	out := render(t,
		Block{Type: BlockText, HTML: `<p>See <a href="cms-article:12">the draft</a></p>`},
		Block{Type: BlockArticle, ArticleID: 13},
		Block{Type: BlockArticle, ArticleID: 12},
	)
	if strings.Contains(out.HTML, "draft-piece") {
		t.Error("an unpublished article was linked")
	}
	if !strings.Contains(out.HTML, "See the draft") {
		t.Error("the link text of an unpublished article should stay as plain text")
	}
	if len(out.Warnings) != 2 {
		t.Fatalf("warnings = %q, want 2 (one per article)", out.Warnings)
	}
	joined := strings.Join(out.Warnings, "\n")
	if !strings.Contains(joined, `Article 12 ("Draft piece") is not published`) || !strings.Contains(joined, "Article 13") {
		t.Errorf("warnings = %q", out.Warnings)
	}
}

func TestRender_EscapesBlockText(t *testing.T) {
	out := render(t,
		Block{Type: BlockHeading, Text: "<script>x</script>"},
		Block{Type: BlockButton, Label: `"><img src=x>`, Href: "https://a.io"},
	)
	if strings.Contains(out.HTML, "<script>x") || strings.Contains(out.HTML, "<img src=x>") {
		t.Errorf("block text was not escaped:\n%s", out.HTML)
	}
	if !strings.Contains(out.HTML, "&lt;script&gt;x&lt;/script&gt;") {
		t.Error("escaped heading text missing")
	}
}

func TestRender_TextBlockIsSanitizedEvenIfStoredRaw(t *testing.T) {
	out := render(t, Block{Type: BlockText, HTML: `<p onclick="x">Hi<script>alert(1)</script></p>`})
	if strings.Contains(out.HTML, "alert(1)") || strings.Contains(out.HTML, "onclick") {
		t.Errorf("render trusted unsanitized text HTML:\n%s", out.HTML)
	}
}

func TestRender_NoWordPressTracking(t *testing.T) {
	out := render(t,
		Block{Type: BlockText, HTML: "<p>x</p>"},
		Block{Type: BlockImage, Src: "https://cdn.example/ad.png", Href: "https://example.com"},
	)
	for _, bad := range []string{"admin-ajax", "tnptr", "doubleclick", "nltr=", "noti=", "cms.thetriangle.org/wp-"} {
		if strings.Contains(out.HTML, bad) {
			t.Errorf("rendered email contains WordPress tracking/remnant %q", bad)
		}
	}
}

func TestRender_FooterHasUnsubscribe(t *testing.T) {
	opts := previewOpts
	opts.UnsubscribeURL = "https://u.example/x?t=abc"
	out, err := Render(context.Background(), Document{Version: 1}, renderFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.HTML, `href="https://u.example/x?t=abc"`) {
		t.Errorf("unsubscribe link missing from an empty newsletter:\n%s", out.HTML)
	}
	if !strings.Contains(out.HTML, ">Unsubscribe<") {
		t.Error("unsubscribe label missing")
	}
}

func TestRender_PreheaderAndSubject(t *testing.T) {
	opts := previewOpts
	opts.Subject = "Week 5 <digest>"
	opts.PreviewText = "Denim Day & more"
	out, err := Render(context.Background(), Document{Version: 1}, renderFixture(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.HTML, "<title>Week 5 &lt;digest&gt;</title>") {
		t.Error("subject not in an escaped <title>")
	}
	if !strings.Contains(out.HTML, "Denim Day &amp; more") {
		t.Error("preheader text missing or unescaped")
	}
}

func TestRender_LinkedArticlesListed(t *testing.T) {
	out := render(t,
		Block{Type: BlockText, HTML: `<a href="cms-article:11">a</a> <a href="cms-article:11">again</a> <a href="cms-article:12">b</a>`},
		Block{Type: BlockArticle, ArticleID: 11},
	)
	if len(out.Articles) != 2 {
		t.Fatalf("articles = %+v, want 11 and 12 once each", out.Articles)
	}
	if out.Articles[0].ID != 11 || !out.Articles[0].Published || out.Articles[0].Title != "Denim Day" {
		t.Errorf("first = %+v", out.Articles[0])
	}
	if out.Articles[1].ID != 12 || out.Articles[1].Published {
		t.Errorf("second = %+v", out.Articles[1])
	}
}

func TestArticleURL(t *testing.T) {
	if got := ArticleURL("https://www.thetriangle.org/", "a b"); got != "https://www.thetriangle.org/article/a%20b" {
		t.Errorf("ArticleURL = %q", got)
	}
}
