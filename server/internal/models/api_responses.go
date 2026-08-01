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

type CommentResponse struct {
	ID           int64      `json:"id"`
	ArticleID    int64      `json:"article_id"`
	ParentID     int64      `json:"parent_id"`
	AuthorName   string     `json:"author_name"`
	AuthorURL    string     `json:"author_url,omitempty"`
	Content      string     `json:"content"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	CreatedAtGMT *time.Time `json:"created_at_gmt,omitempty"`
	Status       string     `json:"status"`
	Type         string     `json:"type"`
}

type CommentInput struct {
	ParentID    int64  `json:"parent_id"`
	AuthorName  string `json:"author_name"`
	AuthorEmail string `json:"author_email"`
	AuthorURL   string `json:"author_url"`
	Content     string `json:"content"`
}

type ArticleCommentsResponse struct {
	ArticleSlug string            `json:"article_slug"`
	Comments    []CommentResponse `json:"comments"`
	TotalCount  int               `json:"total_count"`
}

type AdminCommentResponse struct {
	ID           int64      `json:"id"`
	ArticleID    int64      `json:"article_id"`
	ArticleTitle string     `json:"article_title"`
	ArticleSlug  string     `json:"article_slug"`
	ParentID     int64      `json:"parent_id"`
	AuthorName   string     `json:"author_name"`
	AuthorEmail  string     `json:"author_email,omitempty"`
	AuthorURL    string     `json:"author_url,omitempty"`
	Content      string     `json:"content"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	CreatedAtGMT *time.Time `json:"created_at_gmt,omitempty"`
	Status       string     `json:"status"`
	Type         string     `json:"type"`
}

type AdminCommentsResponse struct {
	Comments   []AdminCommentResponse `json:"comments"`
	Pagination Pagination             `json:"pagination"`
	Counts     map[string]int         `json:"counts"`
}

type CommentStatusPatchRequest struct {
	Status string `json:"status"`
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
	BreakingNews      BreakingNewsSettings      `json:"breaking_news"`
	Carousel          []HomepageCarouselSlide   `json:"carousel"`
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

// BreakingNewsSettings controls the breaking-news banner shown on the public
// homepage: whether it is visible and the text it displays.
type BreakingNewsSettings struct {
	Enabled bool   `json:"enabled"`
	Text    string `json:"text"`
}

type BreakingNewsSettingsResponse = BreakingNewsSettings

type BreakingNewsSettingsPatchRequest = BreakingNewsSettings

// HomepageCarouselSlide is one public Splide carousel item for Scalene's
// homepage. ImageURL may be empty for a text-only slide.
type HomepageCarouselSlide struct {
	Enabled         bool   `json:"enabled"`
	Title           string `json:"title"`
	LinkURL         string `json:"link_url"`
	ImageURL        string `json:"image_url"`
	BackgroundColor string `json:"background_color"`
	TextColor       string `json:"text_color"`
	DesktopOnly     bool   `json:"desktop_only"`
}

type HomepageCarouselSettingsResponse struct {
	Slides []HomepageCarouselSlide `json:"slides"`
}

type HomepageCarouselSettingsPatchRequest = HomepageCarouselSettingsResponse

// SEOSettings holds the site-wide SEO / social defaults.
type SEOSettings struct {
	OGTitle       string `json:"og_title"`
	OGDescription string `json:"og_description"`
	SitemapURL    string `json:"sitemap_url"`
	RobotsURL     string `json:"robots_url"`
}

type SEOSettingsResponse = SEOSettings

type SEOSettingsPatchRequest = SEOSettings

// SEOIssue is a single SEO problem found on a published article.
type SEOIssue struct {
	ArticleID int64  `json:"article_id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Type      string `json:"type"` // "error" | "warning"
	Issue     string `json:"issue"`
}

// SEOAuditResponse is the result of scanning published articles for SEO issues.
// Issues is capped server-side; TotalIssues is the full count before capping.
type SEOAuditResponse struct {
	Issues         []SEOIssue `json:"issues"`
	TotalIssues    int        `json:"total_issues"`
	PublishedCount int        `json:"published_count"`
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

// PollOptionView is one option of a poll, with its share of the vote
// precomputed so every consumer renders the same percentage.
type PollOptionView struct {
	ID         int64   `json:"id"`
	Option     string  `json:"option"`
	Votes      int64   `json:"votes"`
	Percentage float64 `json:"percentage"`
}

// PollView is a full poll: the question, when it ran, and its results.
// StartsAt/EndsAt are RFC3339 strings, or empty when unset -- an absent EndsAt
// is what the public archive renders as "No Expiry".
type PollView struct {
	ID         int64            `json:"id"`
	Question   string           `json:"question"`
	Status     string           `json:"status"`
	StartsAt   string           `json:"starts_at,omitempty"`
	EndsAt     string           `json:"ends_at,omitempty"`
	TotalVotes int64            `json:"total_votes"`
	Options    []PollOptionView `json:"options"`
}

type PollListResponse struct {
	Polls []PollView `json:"polls"`
}

type PollResponse struct {
	Poll PollView `json:"poll"`
}

// PollRequest creates or updates a poll. Pointer fields distinguish "not
// supplied" from "set to empty", which is how a PATCH clears an end date
// (explicit null) without every other PATCH wiping it.
type PollRequest struct {
	Question *string  `json:"question"`
	Status   *string  `json:"status"`
	StartsAt *string  `json:"starts_at"`
	EndsAt   *string  `json:"ends_at"`
	Options  []string `json:"options"`
}

type PollOptionNameRequest struct {
	Option string `json:"option"`
}

type DevelopingStoryRequest struct {
	Title string `json:"title"`
}

type DevelopingStoriesResponse struct {
	Stories []string `json:"stories"`
}

type ActivityEventResponse struct {
	ID        string    `json:"id"`
	User      string    `json:"user"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Date      time.Time `json:"date"`
	UserRole  string    `json:"user_role,omitempty"`
	Message   string    `json:"message,omitempty"`
	Level     string    `json:"level,omitempty"`
	Method    string    `json:"method,omitempty"`
	Path      string    `json:"path,omitempty"`
	RawStatus int       `json:"status,omitempty"`
}

type ActivityResponse struct {
	Events     []ActivityEventResponse `json:"events"`
	TotalCount int                     `json:"total_count"`
}

type MediaResponse struct {
	Media      []Media    `json:"media"`
	Pagination Pagination `json:"pagination"`
}

// MediaGalleryResponse is the trimmed, unpaginated shape used by pickers (the
// featured-image chooser and the editor's insert-image flow).
type MediaGalleryResponse struct {
	Media []MediaOverview `json:"media"`
}

// MediaUploadResponse describes a stored media asset. Path is the canonical
// wp-content-relative path (what to persist as an article's photo_url); URL is
// that path rendered through the configured media base for immediate display.
type MediaUploadResponse struct {
	ID          int64  `json:"id"`
	Path        string `json:"path"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

// MediaIndexResponse reports what a filesystem reindex found. Walked counts
// every filesystem entry visited (the corpus is mostly WordPress derivatives
// that are skipped before any stat, so this is the only counter that moves
// steadily); Scanned counts eligible files; Added counts rows newly inserted;
// Skipped counts files already present in the library.
type MediaIndexResponse struct {
	Walked  int `json:"walked"`
	Scanned int `json:"scanned"`
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
}

// MediaIndexStatusResponse describes an index run. The walk takes minutes on a
// large corpus, far longer than proxies allow a request to hang, so it runs in
// the background and is polled through this shape.
type MediaIndexStatusResponse struct {
	Running    bool               `json:"running"`
	StartedAt  *time.Time         `json:"started_at,omitempty"`
	FinishedAt *time.Time         `json:"finished_at,omitempty"`
	Progress   MediaIndexResponse `json:"progress"`
	Error      string             `json:"error,omitempty"`
}
