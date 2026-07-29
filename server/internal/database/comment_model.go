package database

import "time"

type Comment struct {
	ID           int64
	ArticleID    int64
	ParentID     int64
	AuthorName   string
	AuthorEmail  string
	AuthorURL    string
	AuthorIP     string
	Content      string
	CreatedAt    *time.Time
	CreatedAtGMT *time.Time
	Status       string
	Type         string
}

type AdminComment struct {
	Comment
	ArticleTitle string
	ArticleSlug  string
}

type CreateCommentParams struct {
	ArticleID   int64
	ParentID    int64
	AuthorName  string
	AuthorEmail string
	AuthorURL   string
	AuthorIP    string
	Content     string
	Status      string
	Type        string
	CreatedAt   time.Time
}
