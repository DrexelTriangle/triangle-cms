package newsletter

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestRender_TemplateContextsAreClean guards the ported markup: html/template
// replaces a value it refuses in a URL or CSS context with "ZgotmplZ", which
// would silently ship a broken email.
func TestRender_TemplateContextsAreClean(t *testing.T) {
	yes := true
	doc := Document{Version: 1, Blocks: []Block{
		{Type: BlockText, HTML: `<p>Hello <a href="cms-article:11">story</a> and <a href="mailto:tips@thetriangle.org">tips</a></p>`},
		{Type: BlockHeading, Text: "More From News"},
		{Type: BlockButton, Label: "Read the latest", Href: "https://www.thetriangle.org"},
		{Type: BlockArticle, ArticleID: 11, ShowImage: &yes, ShowExcerpt: &yes},
		{Type: BlockImage, Src: "https://cdn.example/ad.png", Alt: "Ad", Href: "https://example.com/?a=1&b=2"},
		{Type: BlockDivider},
	}}
	out, err := Render(context.Background(), doc, renderFixture(), RenderOptions{
		SiteURL: "https://www.thetriangle.org", Subject: "S", UnsubscribeURL: "https://u.example/x", ManageURL: "#", ViewOnlineURL: "#",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.HTML, "ZgotmplZ") {
		t.Fatalf("html/template rejected a value:\n%s", out.HTML)
	}
	for _, want := range []string{"padding:15px 0px 15px 0px", "padding:20px 0px 50px 0px", `href="mailto:tips@thetriangle.org"`, `href="https://example.com/?a=1&amp;b=2"`} {
		if !strings.Contains(out.HTML, want) {
			t.Errorf("missing %q", want)
		}
	}
	if path := os.Getenv("NEWSLETTER_SAMPLE_OUT"); path != "" {
		if err := os.WriteFile(path, []byte(out.HTML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
