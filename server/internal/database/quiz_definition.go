package database

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"server/internal/models"
)

const (
	maxQuizQuestions   = 30
	maxQuizAnswers     = 6
	minQuizAnswers     = 2
	maxQuizBands       = 4
	minQuizResults     = 2
	maxQuizResults     = 8
	maxQuestionText    = 500
	maxAnswerText      = 200
	maxExplanationText = 500
	maxResultTitle     = 120
	maxResultBody      = 1000
)

var quizResultKeyPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// ValidateQuizDefinition checks the shape every save needs. With strict it
// also checks the rules only a published quiz must satisfy (exactly one correct
// answer, full band coverage, a result on every answer, non-empty titles) and
// raises the one publish warning, a result no answer points at. Warnings never
// block a save.
func ValidateQuizDefinition(kind string, def models.QuizDefinition, strict bool) (errs, warnings []string) {
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if kind != models.QuizKindTrivia && kind != models.QuizKindPersonality {
		add("kind must be trivia or personality")
		return errs, nil
	}
	if len(def.Questions) == 0 {
		add("A quiz needs at least one question")
	}
	if len(def.Questions) > maxQuizQuestions {
		add("A quiz has at most %d questions", maxQuizQuestions)
	}

	resultKeys := map[string]bool{}
	if kind == models.QuizKindPersonality {
		if len(def.Results) < minQuizResults || len(def.Results) > maxQuizResults {
			add("A personality quiz needs 2 to 8 results")
		}
		for i, r := range def.Results {
			if !quizResultKeyPattern.MatchString(r.Key) {
				add("Result %d: key must be 1 to 32 of a-z, 0-9, _ or -", i+1)
			} else if resultKeys[r.Key] {
				add("Result %d: duplicate key %q", i+1, r.Key)
			}
			resultKeys[r.Key] = true
		}
	} else {
		if len(def.Results) > maxQuizBands {
			add("A trivia quiz has at most %d score bands", maxQuizBands)
		}
		for i, r := range def.Results {
			if r.Min == nil || r.Max == nil || *r.Min < 0 {
				add("Result %d: min and max are required", i+1)
			} else if *r.Min > *r.Max {
				add("Result %d: min must not exceed max", i+1)
			}
		}
	}
	for i, r := range def.Results {
		if utf8.RuneCountInString(r.Title) > maxResultTitle {
			add("Result %d: title is over %d characters", i+1, maxResultTitle)
		}
		if utf8.RuneCountInString(r.Body) > maxResultBody {
			add("Result %d: body is over %d characters", i+1, maxResultBody)
		}
		if strict && strings.TrimSpace(r.Title) == "" {
			add("Result %d: title is required", i+1)
		}
	}

	used := map[string]bool{}
	for qi, q := range def.Questions {
		n := qi + 1
		if strings.TrimSpace(q.Text) == "" {
			add("Question %d: text is required", n)
		}
		if utf8.RuneCountInString(q.Text) > maxQuestionText {
			add("Question %d: text is over %d characters", n, maxQuestionText)
		}
		if utf8.RuneCountInString(q.Explanation) > maxExplanationText {
			add("Question %d: explanation is over %d characters", n, maxExplanationText)
		}
		if len(q.Answers) < minQuizAnswers || len(q.Answers) > maxQuizAnswers {
			add("Question %d: 2 to 6 answers are required", n)
		}
		correct := 0
		for ai, a := range q.Answers {
			if strings.TrimSpace(a.Text) == "" {
				add("Question %d, answer %d: text is required", n, ai+1)
			}
			if utf8.RuneCountInString(a.Text) > maxAnswerText {
				add("Question %d, answer %d: text is over %d characters", n, ai+1, maxAnswerText)
			}
			if a.Correct {
				correct++
			}
			switch kind {
			case models.QuizKindTrivia:
				if a.ResultKey != nil {
					add("Question %d, answer %d: result keys belong to personality quizzes", n, ai+1)
				}
			case models.QuizKindPersonality:
				if a.Correct {
					add("Question %d, answer %d: correct answers belong to trivia quizzes", n, ai+1)
				}
				if a.ResultKey == nil {
					if strict {
						add("Question %d, answer %d: a result is required", n, ai+1)
					}
				} else if !resultKeys[*a.ResultKey] {
					add("Question %d, answer %d: no result has key %q", n, ai+1, *a.ResultKey)
				} else {
					used[*a.ResultKey] = true
				}
			}
		}
		if kind == models.QuizKindTrivia {
			if correct > 1 {
				add("Question %d: at most one correct answer", n)
			}
			if strict && correct != 1 {
				add("Question %d: exactly one correct answer is required", n)
			}
		}
		if kind == models.QuizKindPersonality && q.Explanation != "" {
			add("Question %d: explanations belong to trivia quizzes", n)
		}
	}

	if strict && kind == models.QuizKindTrivia && !bandsCover(def.Results, len(def.Questions)) {
		add("Score bands must cover 0 to %d with no gap or overlap", len(def.Questions))
	}
	if strict && kind == models.QuizKindPersonality {
		for _, r := range def.Results {
			if resultKeys[r.Key] && !used[r.Key] {
				warnings = append(warnings, fmt.Sprintf("No answer points at result %q", r.Key))
			}
		}
	}
	return errs, warnings
}

// bandsCover is true when the bands, sorted by min, run from 0 to max with
// each band starting where the previous ended.
func bandsCover(results []models.QuizResult, max int) bool {
	if len(results) == 0 {
		return false
	}
	bands := make([][2]int, 0, len(results))
	for _, r := range results {
		if r.Min == nil || r.Max == nil {
			return false
		}
		bands = append(bands, [2]int{*r.Min, *r.Max})
	}
	sort.Slice(bands, func(i, j int) bool { return bands[i][0] < bands[j][0] })
	next := 0
	for _, b := range bands {
		if b[0] != next || b[1] < b[0] {
			return false
		}
		next = b[1] + 1
	}
	return next == max+1
}

// DeriveQuizBandKeys sets each trivia band's key to "min-max" (spec 4.2). The
// key is display only, so the server writes it rather than trusting the
// editor to keep it in step with min and max. Bands missing either bound keep
// their key; validation reports them.
func DeriveQuizBandKeys(kind string, def *models.QuizDefinition) {
	if kind != models.QuizKindTrivia {
		return
	}
	for i, r := range def.Results {
		if r.Min != nil && r.Max != nil {
			def.Results[i].Key = fmt.Sprintf("%d-%d", *r.Min, *r.Max)
		}
	}
}

// QuizFingerprint describes the shape counts depend on: how many questions,
// how many answers each has, and which answer is correct or which result key
// each answer carries. Text, images and result copy are left out so a typo fix
// never resets counts.
func QuizFingerprint(kind string, def models.QuizDefinition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d", kind, len(def.Questions))
	for _, q := range def.Questions {
		fmt.Fprintf(&b, "|%d", len(q.Answers))
		for _, a := range q.Answers {
			switch {
			case kind == models.QuizKindTrivia && a.Correct:
				b.WriteString(":c")
			case kind == models.QuizKindPersonality && a.ResultKey != nil:
				b.WriteString(":" + *a.ResultKey)
			default:
				b.WriteString(":-")
			}
		}
	}
	return b.String()
}
