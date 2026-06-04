package models

import "time"

type ErrorResponse struct {
	Error string `json:"error"`
}

type AuthRedirectResponse struct {
	Location string `json:"location"`
}

type UserRolePatchRequest struct {
	Role Role `json:"role"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type AuthorSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type CategorySummary struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type TaxonomySummary struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	CanonicalTitle string `json:"canonical_title"`
}

type ArticleListItem struct {
	Title         string            `json:"title"`
	ID            int64             `json:"id"`
	Authors       []AuthorSummary   `json:"authors"`
	Categories    []CategorySummary `json:"categories"`
	Excerpt       string            `json:"excerpt"`
	Slug          string            `json:"slug"`
	Status        ArticleStatus     `json:"status"`
	CommentStatus string            `json:"comment_status"`
	FeaturedImage string            `json:"featured_image"`
	IsFeatured    bool              `json:"is_featured"`
	PublishedDate *time.Time        `json:"published_date,omitempty"`
}

type Pagination struct {
	Page       int  `json:"page"`
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	HasMore    bool `json:"has_more"`
	TotalCount int  `json:"total_count"`
}

type AuthorArticlesAuthor struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type AuthorArticlesResponse struct {
	Author     AuthorArticlesAuthor `json:"author"`
	Articles   []ArticleListItem    `json:"articles"`
	Pagination Pagination           `json:"pagination"`
}

type ArticlesResponse struct {
	Articles   []ArticleListItem `json:"articles"`
	Pagination Pagination        `json:"pagination"`
}

type AuthorsResponse struct {
	Authors    []AuthorOverview `json:"authors"`
	Pagination Pagination       `json:"pagination"`
}

type SectionArticlesResponse struct {
	Section     TaxonomySummary   `json:"section"`
	Subsections []TaxonomySummary `json:"subsections"`
	Articles    []ArticleListItem `json:"articles"`
	Pagination  Pagination        `json:"pagination"`
}

type SubsectionArticlesResponse struct {
	Section    TaxonomySummary   `json:"section"`
	Subsection TaxonomySummary   `json:"subsection"`
	Articles   []ArticleListItem `json:"articles"`
	Pagination Pagination        `json:"pagination"`
}

type SEOResponse struct {
	SEOTitle        string            `json:"seo_title"`
	MetaDescription string            `json:"meta_description"`
	FocusKeyword    string            `json:"focus_keyword"`
	CanonicalURL    string            `json:"canonical_url"`
	Tags            []CategorySummary `json:"tags"`
}

type ArticleDetailResponse struct {
	ID            int64             `json:"id"`
	Title         string            `json:"title"`
	Slug          string            `json:"slug"`
	Content       string            `json:"content"`
	Excerpt       string            `json:"excerpt"`
	Categories    []CategorySummary `json:"categories"`
	CommentStatus string            `json:"comment_status"`
	IsFeatured    bool              `json:"is_featured"`
	Status        ArticleStatus     `json:"status"`
	FeaturedImage string            `json:"featured_image"`
	Authors       []AuthorSummary   `json:"authors"`
	SEO           SEOResponse       `json:"seo"`
	Related       []ArticleListItem `json:"related"`
	PublishedDate *time.Time        `json:"published_date,omitempty"`
}

type HomepageLabel struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type HomepageDevelopingStory struct {
	Slug       string          `json:"slug"`
	Link       string          `json:"link"`
	Title      string          `json:"title"`
	Excerpt    string          `json:"excerpt"`
	ShowInNews bool            `json:"show_in_news"`
	Label      []HomepageLabel `json:"label"`
}

type HomepageResponse struct {
	DevelopingStories []HomepageDevelopingStory `json:"developingstories"`
	News              []ArticleListItem         `json:"news"`
	Opinion           []ArticleListItem         `json:"opinion"`
	Sports            []ArticleListItem         `json:"sports"`
	Entertainment     []ArticleListItem         `json:"entertainment"`
	CAndP             []ArticleListItem         `json:"candp"`
	Columns           []ArticleListItem         `json:"columns"`
}

type SiteSettingsResponse struct {
	SiteTitle string `json:"site_title"`
}

type SiteSettingsPatchRequest struct {
	SiteTitle string `json:"site_title"`
}

type PollOptionRequest struct {
	Option string `json:"option"`
}

type PollOptionRenameRequest struct {
	OldOption string `json:"old_option"`
	NewOption string `json:"new_option"`
}

type PollTitleRequest struct {
	Title string `json:"title"`
}

type PollCountsResponse struct {
	Counts map[string]int64 `json:"counts"`
}

type PollOptionsResponse struct {
	Options []string `json:"options"`
}

type PollTitleResponse struct {
	Title string `json:"title"`
}

type DevelopingStoryRequest struct {
	Title string `json:"title"`
}

type DevelopingStoriesResponse struct {
	Stories []string `json:"stories"`
}
