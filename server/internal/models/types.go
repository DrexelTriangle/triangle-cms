package models

import "time"

type Role string

const (
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

type User struct {
	ID          int64     `json:"id"`
	Sub         string    `json:"-"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Role        Role      `json:"role"`
	AuthorID    *int64    `json:"author_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

type SortDirection string

const (
	SortDirectionAscending  SortDirection = "asc"
	SortDirectionDescending SortDirection = "desc"
)

type AuthorSortBy string

const (
	AuthorSortByDisplayName AuthorSortBy = "display_name"
	AuthorSortByID          AuthorSortBy = "id"
	AuthorSortByCreatedAt   AuthorSortBy = "created_at"
	AuthorSortByUpdatedAt   AuthorSortBy = "updated_at"
)

type ArticleSortBy string

const (
	ArticleSortByTitle         ArticleSortBy = "title"
	ArticleSortBySlug          ArticleSortBy = "slug"
	ArticleSortByCreatedAt     ArticleSortBy = "creation_date"
	ArticleSortByPublishedAt   ArticleSortBy = "published_date"
	ArticleSortByStatus        ArticleSortBy = "status"
	ArticleSortByCommentStatus ArticleSortBy = "comment_status"
)

type MediaSortBy string

const (
	MediaSortByFileName  MediaSortBy = "file_name"
	MediaSortByCreatedAt MediaSortBy = "created_at"
	MediaSortByUpdatedAt MediaSortBy = "updated_at"
	MediaSortBySizeBytes MediaSortBy = "size_bytes"
)

type ArticleStatus string

const (
	ArticleStatusDraft     ArticleStatus = "draft"
	ArticleStatusScheduled ArticleStatus = "scheduled"
	ArticleStatusPublished ArticleStatus = "published"
)

type Author struct {
	ID          int64      `json:"id"`
	Slug        string     `json:"slug"`
	DisplayName string     `json:"display_name"`
	FirstName   string     `json:"first_name,omitempty"`
	LastName    string     `json:"last_name,omitempty"`
	Email       string     `json:"email,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
}

type AuthorOverview struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email,omitempty"`
	ArticleCount int    `json:"article_count"`
	Archived     bool   `json:"archived"`
}

type AuthorInput struct {
	Slug        string `json:"slug,omitempty"`
	DisplayName string `json:"display_name"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Email       string `json:"email,omitempty"`
}

type AuthorPatch struct {
	Slug        *string `json:"slug,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	FirstName   *string `json:"first_name,omitempty"`
	LastName    *string `json:"last_name,omitempty"`
	Email       *string `json:"email,omitempty"`
}

type AuthorListParams struct {
	Limit         int
	Offset        int
	SortBy        AuthorSortBy
	SortDirection SortDirection
	ArticleID     *int64
}

type Article struct {
	Title           string           `json:"title"`
	ID              int64            `json:"id"`
	Authors         []AuthorOverview `json:"authors"`
	Content         string           `json:"content"`
	Categories      []string         `json:"categories"`
	Tags            []string         `json:"-"`
	Excerpt         string           `json:"excerpt"`
	Slug            string           `json:"slug"`
	PhotoURL        string           `json:"photo_url"`
	IsFeatured      bool             `json:"is_featured"`
	BreakingNews    bool             `json:"breaking_news"`
	Status          ArticleStatus    `json:"status"`
	CommentStatus   string           `json:"comment_status"`
	FocusKeyword    string           `json:"focus_keyword"`
	MetaDescription string           `json:"meta_description"`
	SEOTitle        string           `json:"seo_title"`
	CreatedAt       *time.Time       `json:"creation_date,omitempty"`
	PublishedAt     *time.Time       `json:"published_date,omitempty"`
	ScheduledAt     *time.Time       `json:"scheduled_date,omitempty"`
}

type ArticleOverview struct {
	Title         string           `json:"title"`
	ID            int64            `json:"id"`
	Authors       []AuthorOverview `json:"authors"`
	PublishedAt   *time.Time       `json:"published_date,omitempty"`
	Categories    []string         `json:"categories"`
	Excerpt       string           `json:"excerpt"`
	Slug          string           `json:"slug"`
	Status        ArticleStatus    `json:"status"`
	CommentStatus string           `json:"comment_status"`
	PhotoURL      string           `json:"photo_url"`
	IsFeatured    bool             `json:"is_featured"`
	BreakingNews  bool             `json:"breaking_news"`
}

type ArticleInput struct {
	Title           string        `json:"title"`
	Slug            string        `json:"slug,omitempty"`
	Authors         []int64       `json:"authors"`
	Content         string        `json:"content"`
	Excerpt         string        `json:"excerpt,omitempty"`
	Categories      []string      `json:"categories"`
	Tags            []string      `json:"tags,omitempty"`
	PhotoURL        string        `json:"photo_url"`
	IsFeatured      bool          `json:"is_featured"`
	BreakingNews    bool          `json:"breaking_news"`
	Status          ArticleStatus `json:"status"`
	PublishedDate   string        `json:"published_date,omitempty"`
	CommentStatus   string        `json:"comment_status,omitempty"`
	FocusKeyword    string        `json:"focus_keyword,omitempty"`
	MetaDescription string        `json:"meta_description,omitempty"`
	SEOTitle        string        `json:"seo_title,omitempty"`
}

type ArticlePatch struct {
	Title           *string        `json:"title,omitempty"`
	Authors         *[]int64       `json:"authors,omitempty"`
	Content         *string        `json:"content,omitempty"`
	Categories      *[]string      `json:"categories,omitempty"`
	Tags            *[]string      `json:"tags,omitempty"`
	Excerpt         *string        `json:"excerpt,omitempty"`
	PhotoURL        *string        `json:"photo_url,omitempty"`
	IsFeatured      *bool          `json:"is_featured,omitempty"`
	BreakingNews    *bool          `json:"breaking_news,omitempty"`
	Status          *ArticleStatus `json:"status,omitempty"`
	PublishedDate   *string        `json:"published_date,omitempty"`
	CommentStatus   *string        `json:"comment_status,omitempty"`
	FocusKeyword    *string        `json:"focus_keyword,omitempty"`
	MetaDescription *string        `json:"meta_description,omitempty"`
	SEOTitle        *string        `json:"seo_title,omitempty"`
}

type ArticleListParams struct {
	Limit         int
	Offset        int
	Categories    []string
	SortBy        ArticleSortBy
	SortDirection SortDirection
	AuthorID      *int64
	Status        ArticleStatus
}

// Media is one asset in the library. Path is the canonical wp-content-relative
// location on the media filesystem and is the row's stable identity; URL is that
// path rendered through MEDIA_BASE_URL for display and is not stored.
type Media struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	FileName  string `json:"file_name"`
	URL       string `json:"url"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	Width     *int   `json:"width,omitempty"`
	Height    *int   `json:"height,omitempty"`
	AltText   string `json:"alt_text,omitempty"`
	Caption   string `json:"caption,omitempty"`
	// InGallery is the editor's "show this on the public photo gallery" flag.
	// It is off by default: the library is every file on the media mount, house
	// ads and comics included, and the gallery is a curated selection of it.
	InGallery bool       `json:"in_gallery"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type MediaOverview struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	FileName  string `json:"file_name"`
	URL       string `json:"url"`
	MimeType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	Width     *int   `json:"width,omitempty"`
	Height    *int   `json:"height,omitempty"`
	AltText   string `json:"alt_text,omitempty"`
}

type MediaInput struct {
	FileName string `json:"file_name"`
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	AltText  string `json:"alt_text,omitempty"`
	Caption  string `json:"caption,omitempty"`
}

type MediaPatch struct {
	FileName  *string `json:"file_name,omitempty"`
	AltText   *string `json:"alt_text,omitempty"`
	Caption   *string `json:"caption,omitempty"`
	InGallery *bool   `json:"in_gallery,omitempty"`
}

type MediaListParams struct {
	Limit    int
	Offset   int
	Query    string
	MimeType string
	// InGallery filters on the curation flag when set; nil lists the whole
	// library, which is what the editor-facing browser wants.
	InGallery     *bool
	SortBy        MediaSortBy
	SortDirection SortDirection
}

type TaxonomyType string

const (
	TaxonomyTypeSection    TaxonomyType = "section"
	TaxonomyTypeSubsection TaxonomyType = "subsection"
	TaxonomyTypeTag        TaxonomyType = "tag"
)

type TaxonomyItem struct {
	ID             int64   `json:"id"`
	Type           string  `json:"type"`
	Slug           string  `json:"slug"`
	CanonicalTitle string  `json:"canonical_title"`
	ParentSlug     *string `json:"parent_slug,omitempty"`
	ArticleCount   int64   `json:"article_count"`
}

type TaxonomyInput struct {
	Type           string  `json:"type"`
	Slug           string  `json:"slug"`
	CanonicalTitle string  `json:"canonical_title"`
	ParentSlug     *string `json:"parent_slug,omitempty"`
}

type TaxonomyPut struct {
	Slug           string  `json:"slug,omitempty"`
	CanonicalTitle string  `json:"canonical_title"`
	ParentSlug     *string `json:"parent_slug,omitempty"`
}
