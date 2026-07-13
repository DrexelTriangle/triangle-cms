package database

import (
	"context"
	"database/sql"
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
		       COALESCE(seo_title, ''), COALESCE(meta_description, ''), COALESCE(focus_keyword, '')
		FROM articles
		WHERE pub_date IS NOT NULL
		  AND pub_date >= DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? MONTH)
		ORDER BY pub_date DESC, id DESC
	`, auditWindowMonths)
	if err != nil {
		return models.SEOAuditResponse{}, err
	}
	defer rows.Close()

	issues := make([]models.SEOIssue, 0)
	publishedCount := 0
	for rows.Next() {
		var (
			id           int64
			title        string
			slug         string
			seoTitle     string
			metaDesc     string
			focusKeyword string
		)
		if err := rows.Scan(&id, &title, &slug, &seoTitle, &metaDesc, &focusKeyword); err != nil {
			return models.SEOAuditResponse{}, err
		}
		publishedCount++
		for _, issue := range auditArticle(id, slug, title, seoTitle, metaDesc, focusKeyword) {
			issues = append(issues, issue)
		}
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
func auditArticle(id int64, slug, title, seoTitle, metaDesc, focusKeyword string) []models.SEOIssue {
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

	if seoTitle == "" {
		add("warning", "Missing SEO title")
	} else if utf8.RuneCountInString(seoTitle) > seoTitleMaxLen {
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

	return issues
}
