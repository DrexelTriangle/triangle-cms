// Package newsletter holds the campaign body model: a versioned list of
// blocks whose HTML snippets were ported from the WordPress "To The Point"
// template, the sanitizer and article-link locking applied on every save, and
// the renderer that turns a document into email HTML with live article URLs.
package newsletter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Block types. The logo header and the footer (with the unsubscribe link) are
// fixed parts of the layout, not blocks, so an editor cannot remove them.
const (
	BlockText    = "text"
	BlockHeading = "heading"
	BlockButton  = "button"
	BlockArticle = "article"
	BlockImage   = "image"
	BlockDivider = "divider"
)

// Limits on a stored document.
const (
	MaxBlocks        = 100
	MaxTextHTMLBytes = 20 << 10
	MaxShortText     = 200
	MaxURL           = 2048
	MaxDocumentBytes = 512 << 10
)

// Document is the stored campaign body.
type Document struct {
	Version int     `json:"version"`
	Blocks  []Block `json:"blocks"`
}

// Block is one row of the newsletter. Which fields apply depends on Type;
// Normalize clears the rest.
type Block struct {
	Type        string `json:"type"`
	HTML        string `json:"html,omitempty"`
	Text        string `json:"text,omitempty"`
	Label       string `json:"label,omitempty"`
	Href        string `json:"href,omitempty"`
	Src         string `json:"src,omitempty"`
	Alt         string `json:"alt,omitempty"`
	ArticleID   int64  `json:"article_id,omitempty"`
	ShowImage   *bool  `json:"show_image,omitempty"`
	ShowExcerpt *bool  `json:"show_excerpt,omitempty"`
}

// ValidationError is a problem with the document the editor can fix. Handlers
// answer it with 400 and its message.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// ArticleRef is what the renderer needs to know about an article, read fresh
// at render time so a renamed slug or edited title is always current.
type ArticleRef struct {
	ID        int64
	Slug      string
	Title     string
	Excerpt   string
	ImageURL  string
	ImageAlt  string
	Published bool
}

// ArticleLookup resolves article slugs and ids. The database implementation
// lives in internal/database.
type ArticleLookup interface {
	ArticleIDsBySlug(ctx context.Context, slugs []string) (map[string]int64, error)
	ArticlesByID(ctx context.Context, ids []int64) (map[int64]ArticleRef, error)
}

// Parse decodes and validates a stored or submitted document. Empty input is
// an empty version-1 document. Every failure is a *ValidationError.
func Parse(raw []byte) (Document, error) {
	if len(raw) > MaxDocumentBytes {
		return Document{}, invalid("the newsletter body is larger than %d KB", MaxDocumentBytes>>10)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return Document{Version: 1, Blocks: []Block{}}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return Document{}, invalid("the newsletter body is not a valid block document: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Document{}, invalid("the newsletter body has trailing data")
	}
	if doc.Version != 1 {
		return Document{}, invalid("unsupported block document version %d", doc.Version)
	}
	if len(doc.Blocks) > MaxBlocks {
		return Document{}, invalid("a newsletter can have at most %d blocks", MaxBlocks)
	}
	if doc.Blocks == nil {
		doc.Blocks = []Block{}
	}
	for i, b := range doc.Blocks {
		if err := validateBlock(b); err != nil {
			return Document{}, invalid("block %d: %s", i+1, err.Error())
		}
	}
	return doc, nil
}

func validateBlock(b Block) error {
	switch b.Type {
	case BlockText:
		if len(b.HTML) > MaxTextHTMLBytes {
			return fmt.Errorf("text is larger than %d KB", MaxTextHTMLBytes>>10)
		}
	case BlockHeading:
		if err := shortText("heading", b.Text, true); err != nil {
			return err
		}
	case BlockButton:
		if err := shortText("button label", b.Label, true); err != nil {
			return err
		}
		if b.Href == "" {
			return errors.New("a button needs a link")
		}
		if !validHref(b.Href) {
			return fmt.Errorf("button link %q must be an http(s), mailto or article link", clip(b.Href))
		}
	case BlockArticle:
		if b.ArticleID <= 0 {
			return errors.New("an article block needs an article")
		}
	case BlockImage:
		if !validImageSrc(b.Src) {
			return errors.New("an image needs an https source URL")
		}
		if err := shortText("alt text", b.Alt, false); err != nil {
			return err
		}
		if b.Href != "" && !validHref(b.Href) {
			return fmt.Errorf("image link %q must be an http(s), mailto or article link", clip(b.Href))
		}
	case BlockDivider:
	default:
		return fmt.Errorf("unknown block type %q", clip(b.Type))
	}
	return nil
}

func shortText(field, value string, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > MaxShortText {
		return fmt.Errorf("%s is longer than %d characters", field, MaxShortText)
	}
	return nil
}

// validHref accepts http(s) URLs with a host, mailto:, locked article links
// (cms-article:{id}) and site-relative paths ("/classifieds"), which
// Normalize turns into absolute URLs. Whitespace or control characters
// anywhere reject the link: browsers strip them, which is how "java\tscript:"
// tricks work.
func validHref(href string) bool {
	if href == "" || len(href) > MaxURL {
		return false
	}
	for _, r := range href {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	if _, ok := ArticleIDFromHref(href); ok {
		return true
	}
	if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		return true
	}
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != ""
	}
	return false
}

func validImageSrc(src string) bool {
	if src == "" || len(src) > MaxURL || strings.ContainsAny(src, " \t\r\n") {
		return false
	}
	u, err := url.Parse(src)
	return err == nil && strings.EqualFold(u.Scheme, "https") && u.Host != ""
}

func clip(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
