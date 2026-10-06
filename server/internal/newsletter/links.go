package newsletter

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const articleHrefPrefix = "cms-article:"

// ArticleIDFromHref parses a locked article link, cms-article:{id}.
func ArticleIDFromHref(href string) (int64, bool) {
	rest, ok := strings.CutPrefix(href, articleHrefPrefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func lockedHref(id int64) string { return articleHrefPrefix + strconv.FormatInt(id, 10) }

// publicHosts are the hosts whose /article/{slug} URLs are this site's
// articles: the production names plus whatever PUBLIC_SITE_URL points at.
func publicHosts(siteURL string) map[string]bool {
	hosts := map[string]bool{"thetriangle.org": true, "www.thetriangle.org": true}
	if u, err := url.Parse(strings.TrimSpace(siteURL)); err == nil && u.Hostname() != "" {
		hosts[strings.ToLower(u.Hostname())] = true
	}
	return hosts
}

// articleSlug recognises a link to one of the site's articles, absolute or
// site-relative, ignoring query and fragment.
func articleSlug(href string, hosts map[string]bool) (string, bool) {
	u, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	switch {
	case u.Scheme == "" && u.Host == "":
	case (strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) && hosts[strings.ToLower(u.Hostname())]:
	default:
		return "", false
	}
	rest, ok := strings.CutPrefix(u.Path, "/article/")
	if !ok {
		return "", false
	}
	slug := strings.TrimSuffix(rest, "/")
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// Normalize prepares a parsed document for storage:
//   - text HTML goes through the sanitizer;
//   - every link to one of the site's articles, in text, buttons and images,
//     is rewritten to cms-article:{id}, so it follows the article through slug
//     changes and resolves to the live URL at render time;
//   - other site-relative links become absolute against siteURL, because a
//     relative link means nothing in an inbox;
//   - article blocks must name a real article, and default to showing the
//     image and excerpt;
//   - fields that do not belong to a block's type are cleared.
//
// A link to an article that does not exist, or an unknown article id, is a
// *ValidationError. Normalize is idempotent.
func Normalize(ctx context.Context, doc Document, lookup ArticleLookup, siteURL string) (Document, error) {
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	hosts := publicHosts(siteURL)

	// Pass 1: collect every article slug and block id, so each lookup is one
	// query.
	slugHref := map[string]string{}
	collect := func(href string) (string, bool) {
		if slug, ok := articleSlug(href, hosts); ok {
			if _, seen := slugHref[slug]; !seen {
				slugHref[slug] = href
			}
		}
		return href, true
	}
	articleIDs := []int64{}
	for _, b := range doc.Blocks {
		switch b.Type {
		case BlockText:
			sanitize(b.HTML, collect)
		case BlockButton, BlockImage:
			if b.Href != "" {
				collect(b.Href)
			}
		case BlockArticle:
			articleIDs = append(articleIDs, b.ArticleID)
		}
	}

	slugIDs := map[string]int64{}
	if len(slugHref) > 0 {
		slugs := make([]string, 0, len(slugHref))
		for s := range slugHref {
			slugs = append(slugs, s)
		}
		sort.Strings(slugs)
		found, err := lookup.ArticleIDsBySlug(ctx, slugs)
		if err != nil {
			return Document{}, err
		}
		for _, s := range slugs {
			id, ok := found[s]
			if !ok {
				return Document{}, invalid("no article at %s", slugHref[s])
			}
			slugIDs[s] = id
		}
	}
	if len(articleIDs) > 0 {
		found, err := lookup.ArticlesByID(ctx, articleIDs)
		if err != nil {
			return Document{}, err
		}
		for _, id := range articleIDs {
			if _, ok := found[id]; !ok {
				return Document{}, invalid("article %d does not exist", id)
			}
		}
	}

	// Pass 2: rewrite.
	rewrite := func(href string) (string, bool) {
		if slug, ok := articleSlug(href, hosts); ok {
			return lockedHref(slugIDs[slug]), true
		}
		if !validHref(href) {
			return "", false
		}
		if strings.HasPrefix(href, "/") && siteURL != "" {
			return siteURL + href, true
		}
		return href, true
	}
	out := Document{Version: 1, Blocks: make([]Block, 0, len(doc.Blocks))}
	for i, b := range doc.Blocks {
		var nb Block
		switch b.Type {
		case BlockText:
			nb = Block{Type: BlockText, HTML: sanitize(b.HTML, rewrite)}
		case BlockHeading:
			nb = Block{Type: BlockHeading, Text: strings.TrimSpace(b.Text)}
		case BlockButton:
			href, ok := rewrite(b.Href)
			if !ok {
				return Document{}, invalid("block %d: invalid button link", i+1)
			}
			nb = Block{Type: BlockButton, Label: strings.TrimSpace(b.Label), Href: href}
		case BlockArticle:
			nb = Block{Type: BlockArticle, ArticleID: b.ArticleID, ShowImage: boolOr(b.ShowImage, true), ShowExcerpt: boolOr(b.ShowExcerpt, true)}
		case BlockImage:
			nb = Block{Type: BlockImage, Src: b.Src, Alt: strings.TrimSpace(b.Alt)}
			if b.Href != "" {
				href, ok := rewrite(b.Href)
				if !ok {
					return Document{}, invalid("block %d: invalid image link", i+1)
				}
				nb.Href = href
			}
		case BlockDivider:
			nb = Block{Type: BlockDivider}
		default:
			return Document{}, invalid("block %d: unknown block type %q", i+1, clip(b.Type))
		}
		out.Blocks = append(out.Blocks, nb)
	}
	return out, nil
}

func boolOr(v *bool, def bool) *bool {
	if v != nil {
		b := *v
		return &b
	}
	return &def
}
