package newsletter

import (
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
)

// Tags a text block may keep. Everything else is unwrapped: the tag goes, its
// text stays.
var allowedTags = map[string]bool{
	"p": true, "div": true, "br": true, "strong": true, "b": true,
	"em": true, "i": true, "a": true, "ul": true, "ol": true, "li": true,
}

// Elements whose content is dropped along with the tag. Their text is code,
// styling or a foreign document, never something a reader should see.
var droppedWithContent = map[string]bool{
	"script": true, "style": true, "iframe": true, "noscript": true, "template": true,
	"svg": true, "math": true, "object": true, "embed": true, "title": true,
	"textarea": true, "select": true, "head": true, "frameset": true, "noembed": true,
}

// hrefRewriter maps a link target to what should be written out. keep=false
// unwraps the link, leaving its text.
type hrefRewriter func(href string) (string, bool)

func keepValidHref(href string) (string, bool) { return href, validHref(href) }

// SanitizeHTML re-serializes editor HTML through the text-block allowlist:
// p, div, br, strong, b, em, i, ul, ol, li, and a with an http(s), mailto,
// article or site-relative href. All other attributes are dropped. The output
// is balanced: unclosed tags are closed and stray end tags removed.
func SanitizeHTML(in string) string {
	return sanitize(in, keepValidHref)
}

func sanitize(in string, rewrite hrefRewriter) string {
	z := xhtml.NewTokenizer(strings.NewReader(in))
	var (
		out     strings.Builder
		open    []string // emitted, not yet closed
		dropped int      // depth inside a droppedWithContent element
	)
	for {
		tt := z.Next()
		switch tt {
		case xhtml.ErrorToken:
			for i := len(open) - 1; i >= 0; i-- {
				out.WriteString("</" + open[i] + ">")
			}
			return out.String()

		case xhtml.TextToken:
			if dropped == 0 {
				out.WriteString(html.EscapeString(string(z.Text())))
			}

		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			tok := z.Token()
			name := tok.Data
			if droppedWithContent[name] {
				if tt == xhtml.StartTagToken {
					dropped++
				}
				continue
			}
			if dropped > 0 || !allowedTags[name] {
				continue
			}
			if name == "br" {
				out.WriteString("<br>")
				continue
			}
			if name == "a" {
				href, ok := "", false
				for _, attr := range tok.Attr {
					if attr.Key == "href" {
						href, ok = rewrite(attr.Val)
						break
					}
				}
				if !ok {
					continue // unwrap: no tag, text kept
				}
				out.WriteString(`<a href="` + html.EscapeString(href) + `">`)
			} else {
				out.WriteString("<" + name + ">")
			}
			if tt == xhtml.StartTagToken {
				open = append(open, name)
			} else {
				out.WriteString("</" + name + ">")
			}

		case xhtml.EndTagToken:
			tok := z.Token()
			name := tok.Data
			if droppedWithContent[name] {
				if dropped > 0 {
					dropped--
				}
				continue
			}
			if dropped > 0 || !allowedTags[name] || name == "br" {
				continue
			}
			idx := -1
			for i := len(open) - 1; i >= 0; i-- {
				if open[i] == name {
					idx = i
					break
				}
			}
			if idx < 0 {
				continue // stray end tag, or the end of an unwrapped link
			}
			for i := len(open) - 1; i >= idx; i-- {
				out.WriteString("</" + open[i] + ">")
			}
			open = open[:idx]
		}
		// Comments and doctypes are dropped.
	}
}
