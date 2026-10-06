package newsletter

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	xhtml "golang.org/x/net/html"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Fixed template assets. They point at the WordPress-era files Scalene still
// proxies; the send work should move them into the media library before the
// first real send, because the proxy dies with WordPress.
const (
	logoPath          = "/proxy/wp-content/uploads/newsletter/thumbnails/2024/12/nl-header-1-1200x0.png"
	twitterIconPath   = "/proxy/wp-content/plugins/newsletter/images/social-4/twitter.png"
	instagramIconPath = "/proxy/wp-content/plugins/newsletter/images/social-4/instagram.png"
)

// RenderOptions carries what changes per render. The preview passes "#" for
// the three per-recipient links; the send passes token URLs.
type RenderOptions struct {
	SiteURL        string
	Subject        string
	PreviewText    string
	UnsubscribeURL string
	ManageURL      string
	ViewOnlineURL  string
}

// LinkedArticle is an article the document points at, for the editor's
// "locked to" chips.
type LinkedArticle struct {
	ID        int64
	Title     string
	Published bool
}

// Rendered is a finished email body plus what the editor should know about it.
// The send must refuse while Warnings is non-empty.
type Rendered struct {
	HTML     string
	Warnings []string
	Articles []LinkedArticle
}

// ArticleURL is an article's public permalink. It is derived from the current
// slug at render time, which is what keeps a locked link pointing at the live
// page after a rename.
func ArticleURL(siteURL, slug string) string {
	return strings.TrimRight(siteURL, "/") + "/article/" + url.PathEscape(slug)
}

type layoutData struct {
	Subject          string
	PreviewText      string
	SiteURL          string
	LogoURL          string
	TwitterIconURL   string
	InstagramIconURL string
	UnsubscribeURL   string
	ManageURL        string
	ViewOnlineURL    string
	Blocks           []template.HTML
}

type articleData struct {
	URL, Title, Excerpt, ImageURL, ImageAlt string
}

// Render turns a normalized document into email HTML. Locked article links
// and article blocks are resolved against the current article rows. An
// article that is missing or not published is never linked: its links fall
// back to plain text, its blocks are left out, and a warning says so.
func Render(ctx context.Context, doc Document, lookup ArticleLookup, opts RenderOptions) (Rendered, error) {
	siteURL := strings.TrimRight(strings.TrimSpace(opts.SiteURL), "/")

	// Every article the document references, in first-seen order.
	order := []int64{}
	seen := map[int64]bool{}
	note := func(id int64) {
		if id > 0 && !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	noteHref := func(href string) (string, bool) {
		if id, ok := ArticleIDFromHref(href); ok {
			note(id)
		}
		return href, true
	}
	for _, b := range doc.Blocks {
		switch b.Type {
		case BlockText:
			sanitize(b.HTML, noteHref)
		case BlockButton, BlockImage:
			noteHref(b.Href)
		case BlockArticle:
			note(b.ArticleID)
		}
	}
	refs := map[int64]ArticleRef{}
	if len(order) > 0 {
		found, err := lookup.ArticlesByID(ctx, order)
		if err != nil {
			return Rendered{}, err
		}
		refs = found
	}

	out := Rendered{Warnings: []string{}, Articles: []LinkedArticle{}}
	for _, id := range order {
		ref, ok := refs[id]
		switch {
		case !ok:
			out.Warnings = append(out.Warnings, fmt.Sprintf("Article %d no longer exists", id))
		case !ref.Published:
			out.Warnings = append(out.Warnings, fmt.Sprintf("Article %d (%q) is not published", id, ref.Title))
			out.Articles = append(out.Articles, LinkedArticle{ID: id, Title: ref.Title})
		default:
			out.Articles = append(out.Articles, LinkedArticle{ID: id, Title: ref.Title, Published: true})
		}
	}

	// live resolves a link for the email: locked article links become the
	// article's current URL, or nothing if it is not live.
	live := func(href string) (string, bool) {
		if id, ok := ArticleIDFromHref(href); ok {
			ref, found := refs[id]
			if !found || !ref.Published {
				return "", false
			}
			return ArticleURL(siteURL, ref.Slug), true
		}
		return keepValidHref(href)
	}

	data := layoutData{
		Subject:          opts.Subject,
		PreviewText:      opts.PreviewText,
		SiteURL:          siteURL,
		LogoURL:          siteURL + logoPath,
		TwitterIconURL:   siteURL + twitterIconPath,
		InstagramIconURL: siteURL + instagramIconPath,
		UnsubscribeURL:   opts.UnsubscribeURL,
		ManageURL:        opts.ManageURL,
		ViewOnlineURL:    opts.ViewOnlineURL,
	}
	for _, b := range doc.Blocks {
		name, value, ok := blockView(b, refs, siteURL, live)
		if !ok {
			continue
		}
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, name, value); err != nil {
			return Rendered{}, err
		}
		data.Blocks = append(data.Blocks, template.HTML(buf.String()))
	}
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "layout", data); err != nil {
		return Rendered{}, err
	}
	out.HTML = buf.String()
	return out, nil
}

// blockView picks a block's template and the value it renders. ok=false
// leaves the block out of the email.
func blockView(b Block, refs map[int64]ArticleRef, siteURL string, live hrefRewriter) (string, any, bool) {
	switch b.Type {
	case BlockText:
		// Sanitized again here, not trusted from storage: the template marks
		// this HTML safe, so it must not depend on every writer having
		// normalized it.
		return "text", struct{ HTML template.HTML }{template.HTML(sanitize(b.HTML, live))}, true
	case BlockHeading:
		return "heading", b, true
	case BlockButton:
		href, ok := live(b.Href)
		if !ok {
			return "", nil, false
		}
		return "button", struct{ Label, Href string }{b.Label, href}, true
	case BlockArticle:
		ref, ok := refs[b.ArticleID]
		if !ok || !ref.Published {
			return "", nil, false
		}
		d := articleData{URL: ArticleURL(siteURL, ref.Slug), Title: ref.Title}
		if b.ShowImage == nil || *b.ShowImage {
			d.ImageURL, d.ImageAlt = ref.ImageURL, ref.ImageAlt
		}
		if b.ShowExcerpt == nil || *b.ShowExcerpt {
			d.Excerpt = plainText(ref.Excerpt)
		}
		return "article", d, true
	case BlockImage:
		href := ""
		if b.Href != "" {
			href, _ = live(b.Href) // an unpublished target leaves the image unlinked
		}
		return "image", struct{ Src, Alt, Href string }{b.Src, b.Alt, href}, true
	case BlockDivider:
		return "divider", nil, true
	}
	return "", nil, false
}

// plainText strips markup from an excerpt, keeping its text. WordPress-era
// excerpts can carry tags, and they render as text in the article block.
func plainText(s string) string {
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case xhtml.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case xhtml.StartTagToken:
			name, _ := z.TagName()
			if droppedWithContent[string(name)] {
				skip++
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			if droppedWithContent[string(name)] && skip > 0 {
				skip--
			} else {
				b.WriteByte(' ')
			}
		}
	}
}
