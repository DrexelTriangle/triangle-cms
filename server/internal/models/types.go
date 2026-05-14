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
}

type AuthorOverview struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email,omitempty"`
	ArticleCount int    `json:"article_count"`
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
	Title         string           `json:"title"`
	ID            int64            `json:"id"`
	Authors       []AuthorOverview `json:"authors"`
	Content       string           `json:"content"`
	Categories    []string         `json:"categories"`
	Tags          []string         `json:"-"`
	Excerpt       string           `json:"excerpt"`
	Slug          string           `json:"slug"`
	PhotoURL      string           `json:"photo_url"`
	IsFeatured    bool             `json:"is_featured"`
	Status        ArticleStatus    `json:"status"`
	CommentStatus string           `json:"comment_status"`
	CreatedAt     *time.Time       `json:"creation_date,omitempty"`
	PublishedAt   *time.Time       `json:"published_date,omitempty"`
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
}

type ArticleInput struct {
	Title         string        `json:"title"`
	Slug          string        `json:"slug,omitempty"`
	Authors       []int64       `json:"authors"`
	Content       string        `json:"content"`
	Categories    []string      `json:"categories"`
	PhotoURL      string        `json:"photo_url"`
	IsFeatured    bool          `json:"is_featured"`
	Status        ArticleStatus `json:"status"`
	CommentStatus string        `json:"comment_status,omitempty"`
}

type ArticlePatch struct {
	Title         *string        `json:"title,omitempty"`
	Authors       *[]int64       `json:"authors,omitempty"`
	Content       *string        `json:"content,omitempty"`
	Categories    *[]string      `json:"categories,omitempty"`
	Excerpt       *string        `json:"excerpt,omitempty"`
	PhotoURL      *string        `json:"photo_url,omitempty"`
	IsFeatured    *bool          `json:"is_featured,omitempty"`
	Status        *ArticleStatus `json:"status,omitempty"`
	CommentStatus *string        `json:"comment_status,omitempty"`
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

type Media struct {
	ID        int64      `json:"id"`
	FileName  string     `json:"file_name"`
	URL       string     `json:"url"`
	MimeType  string     `json:"mime_type"`
	SizeBytes int64      `json:"size_bytes"`
	Width     *int       `json:"width,omitempty"`
	Height    *int       `json:"height,omitempty"`
	AltText   string     `json:"alt_text,omitempty"`
	Caption   string     `json:"caption,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type MediaOverview struct {
	ID        int64  `json:"id"`
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
	FileName *string `json:"file_name,omitempty"`
	AltText  *string `json:"alt_text,omitempty"`
	Caption  *string `json:"caption,omitempty"`
}

type MediaListParams struct {
	Limit         int
	Offset        int
	Query         string
	MimeType      string
	SortBy        MediaSortBy
	SortDirection SortDirection
}

type TaxonomyKind string

const (
	TaxonomyKindSection    TaxonomyKind = "section"
	TaxonomyKindSubsection TaxonomyKind = "subsection"
	TaxonomyKindTag        TaxonomyKind = "tag"
)

type TaxonomyItem struct {
	ID             int64   `json:"id"`
	Kind           string  `json:"kind"`
	Slug           string  `json:"slug"`
	CanonicalTitle string  `json:"canonical_title"`
	ParentSlug     *string `json:"parent_slug,omitempty"`
}

type TaxonomyInput struct {
	Kind           string  `json:"kind"`
	Slug           string  `json:"slug"`
	CanonicalTitle string  `json:"canonical_title"`
	ParentSlug     *string `json:"parent_slug,omitempty"`
}

type TaxonomyPut struct {
	Slug           string  `json:"slug,omitempty"`
	CanonicalTitle string  `json:"canonical_title"`
	ParentSlug     *string `json:"parent_slug,omitempty"`
}
