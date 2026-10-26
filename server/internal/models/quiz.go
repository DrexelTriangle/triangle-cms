package models

const (
	QuizKindTrivia      = "trivia"
	QuizKindPersonality = "personality"
	QuizStatusDraft     = "draft"
	QuizStatusPublished = "published"
)

type QuizAnswer struct {
	Text      string  `json:"text"`
	Correct   bool    `json:"correct"`
	ResultKey *string `json:"result_key"`
}

type QuizQuestion struct {
	Text         string       `json:"text"`
	ImageMediaID *int64       `json:"image_media_id"`
	ImageURL     *string      `json:"image_url,omitempty"`
	Answers      []QuizAnswer `json:"answers"`
	Explanation  string       `json:"explanation"`
}

// Min and Max are trivia score bands; Key is the personality result key.
// Both kinds share one shape so the site has one renderer.
type QuizResult struct {
	Key          string  `json:"key"`
	Min          *int    `json:"min,omitempty"`
	Max          *int    `json:"max,omitempty"`
	Title        string  `json:"title"`
	Body         string  `json:"body"`
	ImageMediaID *int64  `json:"image_media_id"`
	ImageURL     *string `json:"image_url,omitempty"`
}

type QuizDefinition struct {
	Questions []QuizQuestion `json:"questions"`
	Results   []QuizResult   `json:"results"`
}

type Quiz struct {
	ID                int64          `json:"id"`
	Slug              string         `json:"slug"`
	Title             string         `json:"title"`
	Kind              string         `json:"kind"`
	Status            string         `json:"status"`
	Intro             string         `json:"intro"`
	CoverMediaID      *int64         `json:"cover_media_id"`
	CoverURL          *string        `json:"cover_url"`
	Definition        QuizDefinition `json:"definition"`
	DefinitionVersion int            `json:"definition_version"`
	EditVersion       int            `json:"edit_version"`
	Counts            [][]int64      `json:"counts"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
}

// QuizSaveResponse is what POST and PATCH return: the saved quiz plus any
// non-blocking warnings from the save's validation.
type QuizSaveResponse struct {
	Quiz
	Warnings []string `json:"warnings,omitempty"`
}

type QuizListItem struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	UpdatedAt   string `json:"updated_at"`
	Starts      int64  `json:"starts"`
	EditVersion int    `json:"edit_version"`
}

type QuizInput struct {
	Slug         string         `json:"slug"`
	Title        string         `json:"title"`
	Kind         string         `json:"kind"`
	Status       string         `json:"status"`
	Intro        string         `json:"intro"`
	CoverMediaID *int64         `json:"cover_media_id"`
	Definition   QuizDefinition `json:"definition"`
	EditVersion  int            `json:"edit_version"`
}

type QuizAnswerInput struct {
	DefinitionVersion int `json:"definition_version"`
	QuestionIndex     int `json:"question_index"`
	AnswerIndex       int `json:"answer_index"`
}

type QuizValidationResponse struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

type QuizConflictResponse struct {
	Reason string `json:"reason"`
}

type QuizReferencesResponse struct {
	Published int `json:"published"`
	Scheduled int `json:"scheduled"`
	Draft     int `json:"draft"`
}

type QuizAnswerCountsResponse struct {
	Counts []int64 `json:"counts"`
}
