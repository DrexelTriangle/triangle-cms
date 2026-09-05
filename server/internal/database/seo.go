package database

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"unicode/utf8"

	"server/internal/models"
)

// auditWindowMonths scopes the SEO audit to recently published articles so the
// imported legacy backlog (which predates structured SEO metadata) doesn't drown
// out current, fixable issues. Mirrored by the frontend (seoView.tsx).
const auditWindowMonths = 24

// maxAuditIssues caps how many issues are returned to the client. TotalIssues in
// the response reflects the full, uncapped count.
const maxAuditIssues = 200

// SEO metadata length guidance, in characters.
const (
	seoTitleMaxLen = 60
	metaDescMinLen = 50
	metaDescMaxLen = 160
)

// GetSEOAudit scans articles published within the audit window and reports SEO
// metadata problems. Articles are returned most-recent first so the capped slice
// surfaces the freshest issues.
func GetSEOAudit(ctx context.Context, conn *sql.DB) (models.SEOAuditResponse, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(slug, ''),
		       COALESCE(seo_title, ''), COALESCE(meta_description, ''), COALESCE(focus_keyword, ''),
		       COALESCE(photo_url, ''), COALESCE(excerpt, '')
		FROM articles
		WHERE pub_date IS NOT NULL
		  AND pub_date <= UTC_TIMESTAMP()
		  AND pub_date >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? MONTH)
		  AND archived_at IS NULL
		  AND COALESCE(noindex, 0) = 0
		ORDER BY pub_date DESC, id DESC
	`, auditWindowMonths)
	if err != nil {
		return models.SEOAuditResponse{}, err
	}
	defer rows.Close()

	issues := make([]models.SEOIssue, 0)
	publishedCount := 0
	// Meta descriptions duplicated across articles make the pages compete for
	// the same snippet, so the first article using a description is recorded and
	// every later one is reported against it.
	firstUseOfMetaDesc := make(map[string]string)
	for rows.Next() {
		var (
			id           int64
			title        string
			slug         string
			seoTitle     string
			metaDesc     string
			focusKeyword string
			photoURL     string
			excerpt      string
		)
		if err := rows.Scan(&id, &title, &slug, &seoTitle, &metaDesc, &focusKeyword, &photoURL, &excerpt); err != nil {
			return models.SEOAuditResponse{}, err
		}
		publishedCount++
		articleIssues := auditArticle(id, slug, title, seoTitle, metaDesc, focusKeyword, photoURL, excerpt)

		if key := strings.ToLower(strings.TrimSpace(metaDesc)); key != "" {
			if firstSlug, seen := firstUseOfMetaDesc[key]; seen {
				articleIssues = append(articleIssues, models.SEOIssue{
					ArticleID: id,
					Slug:      slug,
					Title:     title,
					Type:      "warning",
					Issue:     "Meta description duplicates \"" + firstSlug + "\"",
				})
			} else {
				firstUseOfMetaDesc[key] = slug
			}
		}

		issues = append(issues, articleIssues...)
	}
	if err := rows.Err(); err != nil {
		return models.SEOAuditResponse{}, err
	}

	totalIssues := len(issues)
	if totalIssues > maxAuditIssues {
		issues = issues[:maxAuditIssues]
	}

	return models.SEOAuditResponse{
		Issues:         issues,
		TotalIssues:    totalIssues,
		PublishedCount: publishedCount,
	}, nil
}

// auditArticle returns the SEO issues for a single article.
func auditArticle(id int64, slug, title, seoTitle, metaDesc, focusKeyword, photoURL, excerpt string) []models.SEOIssue {
	var issues []models.SEOIssue
	add := func(issueType, message string) {
		issues = append(issues, models.SEOIssue{
			ArticleID: id,
			Slug:      slug,
			Title:     title,
			Type:      issueType,
			Issue:     message,
		})
	}

	seoTitle = strings.TrimSpace(seoTitle)
	metaDesc = strings.TrimSpace(metaDesc)
	focusKeyword = strings.TrimSpace(focusKeyword)

	// A blank seo_title is not a defect: the public site renders the article
	// title in its place (ArticleLayout.astro), so the page ships a correct
	// <title> either way. Only the effective title's length matters, because
	// that is what search results actually truncate.
	effectiveTitle := seoTitle
	if effectiveTitle == "" {
		effectiveTitle = strings.TrimSpace(title)
	}
	switch {
	case effectiveTitle == "":
		add("warning", "Missing SEO title")
	case utf8.RuneCountInString(effectiveTitle) > seoTitleMaxLen:
		add("warning", "SEO title exceeds 60 characters")
	}

	switch {
	case metaDesc == "":
		add("error", "Missing meta description")
	case utf8.RuneCountInString(metaDesc) < metaDescMinLen:
		add("warning", "Meta description is shorter than 50 characters")
	case utf8.RuneCountInString(metaDesc) > metaDescMaxLen:
		add("warning", "Meta description exceeds 160 characters")
	}

	if focusKeyword == "" {
		add("warning", "Missing focus keyword")
	}

	// A missing featured image costs more than a thumbnail: the public site has
	// no og:image to fall back on beyond the site logo, and the NewsArticle
	// structured data carries an `image` array Google treats as required.
	if !hasFeaturedImage(photoURL) {
		add("error", "Missing featured image (no social preview or article image)")
	}

	// The excerpt is what the public site falls back to when meta_description is
	// blank, so an article missing both has no description at all.
	if strings.TrimSpace(excerpt) == "" && metaDesc == "" {
		add("error", "No meta description and no excerpt to fall back on")
	}

	return issues
}

// hasFeaturedImage reports whether photo_url names a real image. Articles
// imported from WordPress without a thumbnail carry the string "-1" rather than
// NULL or an empty value, so that sentinel counts as missing.
func hasFeaturedImage(photoURL string) bool {
	trimmed := strings.TrimSpace(photoURL)
	return trimmed != "" && trimmed != "-1"
}

// yoastVariablePattern matches a Yoast template variable: "%%title%%".
var yoastVariablePattern = regexp.MustCompile(`%%[a-zA-Z0-9_]+%%`)

// yoastGluedSeparatorPattern finds a separator an editor typed with no space
// before the variable that follows it ("Pars pro Toto -%%sitename%%").
var yoastGluedSeparatorPattern = regexp.MustCompile(`([-|\x{2013}\x{2014}])(%%)`)

// yoastSeparatorRunPattern collapses separators a dropped variable has left
// sitting next to each other.
var yoastSeparatorRunPattern = regexp.MustCompile(`(?:\s*-\s*){2,}`)

// HasYoastVariables reports whether a value still carries an unexpanded Yoast
// template variable.
func HasYoastVariables(value string) bool {
	return yoastVariablePattern.MatchString(value)
}

// ExpandYoastTitle turns a Yoast SEO title template into finished text.
//
// WordPress stored an article's SEO title as a template rather than as a title
// ("%%title%% %%page%%", or a headline with "%%page%% %%sep%% %%sitename%%"
// appended) and Yoast substituted the variables when it rendered the page.
// Nothing substitutes them here, so a template copied into seo_title verbatim
// reaches the public site's <title>, og:title and twitter:title as the literal
// tokens. Mirrors expandSeoVariables in Scalene's src/utils/seoTemplate.ts; the
// two are meant to agree.
//
// %%page%% expands to nothing, as it did under Yoast: it numbers the pages of a
// paginated archive, and an article is one page. A variable this corpus does
// not carry is dropped rather than left visible: an unrecognised token is
// still a token, and printing it is the defect.
//
// Returns "" when nothing usable survives, which callers store as a blank
// seo_title: the public site then renders the headline, which is what Yoast's
// default template meant in the first place.
func ExpandYoastTitle(template, title, siteTitle, primaryCategory string) string {
	if template == "" {
		return ""
	}

	spaced := yoastGluedSeparatorPattern.ReplaceAllString(template, "$1 $2")

	const sep = "-"
	expanded := yoastVariablePattern.ReplaceAllStringFunc(spaced, func(token string) string {
		switch strings.ToLower(strings.Trim(token, "%")) {
		case "title":
			return strings.TrimSpace(title)
		case "sitename":
			return strings.TrimSpace(siteTitle)
		case "primary_category":
			return strings.TrimSpace(primaryCategory)
		case "sep":
			return sep
		default:
			return ""
		}
	})

	return tidyYoastSeparators(expanded, sep)
}

// tidyYoastSeparators cleans up after a variable that expanded to nothing: the
// punctuation framing it is still there, so a template ending in %%sep%% leaves
// the title hanging on a dash and a dropped %%page%% leaves two separators in a
// row. Returns "" when only punctuation survived.
func tidyYoastSeparators(value, sep string) string {
	collapsed := strings.Join(strings.Fields(value), " ")
	collapsed = yoastSeparatorRunPattern.ReplaceAllString(collapsed, " "+sep+" ")
	collapsed = strings.TrimSpace(strings.TrimPrefix(collapsed, sep))
	collapsed = strings.TrimSpace(strings.TrimSuffix(collapsed, sep))
	if collapsed == sep {
		return ""
	}
	return collapsed
}

// isRedundantSEOTitle reports whether an expanded title says no more than the
// public site would render on its own: the headline, or the headline with the
// publication name appended, which is the fallback the article page already
// builds (ArticleLayout.astro).
func isRedundantSEOTitle(expanded, title, siteTitle string) bool {
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(value), " "))
	}
	candidate := normalize(expanded)
	return candidate == "" ||
		candidate == normalize(title) ||
		candidate == normalize(title+" - "+siteTitle)
}

// ExpandYoastTitleTemplates rewrites every seo_title still holding a Yoast
// template into the text Yoast would have rendered.
//
// Runs after the Yoast backfill rather than inside it: the backfill only fills
// blanks and records a one-time flag, so on a database seeded before this
// existed the templates are already in place and would never be revisited.
// Idempotent: expanded text carries no variables, so a second pass matches
// nothing.
func ExpandYoastTitleTemplates(ctx context.Context, conn *sql.DB) (int, error) {
	siteTitle, err := GetSiteTitle(ctx, conn)
	if err != nil {
		return 0, err
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(seo_title, ''),
		       COALESCE(JSON_VALUE(categories, '$[0]'), '')
		FROM articles
		WHERE seo_title REGEXP '%%[a-zA-Z0-9_]+%%'
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type rewrite struct {
		id       int64
		seoTitle string
	}
	var pending []rewrite
	for rows.Next() {
		var id int64
		var title, seoTitle, primaryCategory string
		if err := rows.Scan(&id, &title, &seoTitle, &primaryCategory); err != nil {
			return 0, err
		}
		expanded := ExpandYoastTitle(seoTitle, title, siteTitle, primaryCategory)
		// Yoast's default template is the headline, so expanding it stores a
		// custom SEO title that says what the article already says, and then
		// stops tracking the headline when an editor rewrites it. Blank means
		// "use the headline", which is both what the template meant and what the
		// SEO audit treats as fine (see auditArticle).
		if isRedundantSEOTitle(expanded, title, siteTitle) {
			expanded = ""
		}
		pending = append(pending, rewrite{id: id, seoTitle: expanded})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, row := range pending {
		if _, err := conn.ExecContext(ctx,
			"UPDATE articles SET seo_title = ? WHERE id = ?", row.seoTitle, row.id,
		); err != nil {
			return 0, err
		}
	}

	return len(pending), nil
}
