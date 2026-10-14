package database

import (
	"strings"
	"testing"

	"server/internal/models"
)

func ptr[T any](v T) *T { return &v }

func trivia(n int) models.QuizDefinition {
	def := models.QuizDefinition{Results: []models.QuizResult{{Min: ptr(0), Max: ptr(n), Title: "Band"}}}
	for i := 0; i < n; i++ {
		def.Questions = append(def.Questions, models.QuizQuestion{
			Text:    "Q",
			Answers: []models.QuizAnswer{{Text: "a", Correct: true}, {Text: "b"}},
		})
	}
	return def
}

func personality() models.QuizDefinition {
	return models.QuizDefinition{
		Questions: []models.QuizQuestion{{
			Text:    "Q",
			Answers: []models.QuizAnswer{{Text: "a", ResultKey: ptr("owl")}, {Text: "b", ResultKey: ptr("cat")}},
		}},
		Results: []models.QuizResult{{Key: "owl", Title: "Owl"}, {Key: "cat", Title: "Cat"}},
	}
}

func TestValidateQuizDefinition(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		strict  bool
		mutate  func(*models.QuizDefinition)
		wantErr string
	}{
		{"valid trivia strict", models.QuizKindTrivia, true, func(d *models.QuizDefinition) {}, ""},
		{"valid personality strict", models.QuizKindPersonality, true, nil, ""},
		{"no questions", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Questions = nil }, "at least one question"},
		{"too many questions", models.QuizKindTrivia, false, func(d *models.QuizDefinition) {
			for len(d.Questions) < 31 {
				d.Questions = append(d.Questions, d.Questions[0])
			}
		}, "at most 30 questions"},
		{"one answer", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Questions[0].Answers = d.Questions[0].Answers[:1] }, "Question 1: 2 to 6 answers"},
		{"empty answer text", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Questions[0].Answers[1].Text = " " }, "Question 1, answer 2: text is required"},
		{"two correct", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Questions[0].Answers[1].Correct = true }, "Question 1: at most one correct answer"},
		{"no correct draft ok", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Questions[0].Answers[0].Correct = false }, ""},
		{"no correct strict", models.QuizKindTrivia, true, func(d *models.QuizDefinition) { d.Questions[0].Answers[0].Correct = false }, "Question 1: exactly one correct answer"},
		{"trivia with result key", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Questions[0].Answers[0].ResultKey = ptr("x") }, "Question 1, answer 1: result keys belong to personality quizzes"},
		{"band gap strict", models.QuizKindTrivia, true, func(d *models.QuizDefinition) { d.Results[0].Max = ptr(1) }, "Score bands must cover 0 to 2"},
		{"band gap draft ok", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Results[0].Max = ptr(1) }, ""},
		{"band min above max", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Results[0].Min = ptr(3) }, "Result 1: min must not exceed max"},
		{"five bands", models.QuizKindTrivia, false, func(d *models.QuizDefinition) {
			for len(d.Results) < 5 {
				d.Results = append(d.Results, d.Results[0])
			}
		}, "at most 4 score bands"},
		{"empty band title strict", models.QuizKindTrivia, true, func(d *models.QuizDefinition) { d.Results[0].Title = "" }, "Result 1: title is required"},
		{"personality correct set", models.QuizKindPersonality, false, func(d *models.QuizDefinition) { d.Questions[0].Answers[0].Correct = true }, "Question 1, answer 1: correct answers belong to trivia quizzes"},
		{"personality unknown key", models.QuizKindPersonality, false, func(d *models.QuizDefinition) { d.Questions[0].Answers[0].ResultKey = ptr("dog") }, `Question 1, answer 1: no result has key "dog"`},
		{"personality bad key", models.QuizKindPersonality, false, func(d *models.QuizDefinition) { d.Results[0].Key = "Owl!" }, "Result 1: key must be 1 to 32 of a-z, 0-9, _ or -"},
		{"personality duplicate key", models.QuizKindPersonality, false, func(d *models.QuizDefinition) { d.Results[1].Key = "owl" }, "Result 2: duplicate key"},
		{"personality one result", models.QuizKindPersonality, false, func(d *models.QuizDefinition) {
			d.Results = d.Results[:1]
			d.Questions[0].Answers[1].ResultKey = ptr("owl")
		}, "2 to 8 results"},
		{"personality missing key draft ok", models.QuizKindPersonality, false, func(d *models.QuizDefinition) { d.Questions[0].Answers[1].ResultKey = nil }, ""},
		{"personality missing key strict", models.QuizKindPersonality, true, func(d *models.QuizDefinition) { d.Questions[0].Answers[1].ResultKey = nil }, "Question 1, answer 2: a result is required"},
		{"personality explanation", models.QuizKindPersonality, false, func(d *models.QuizDefinition) { d.Questions[0].Explanation = "x" }, "Question 1: explanations belong to trivia quizzes"},
		{"question text too long", models.QuizKindTrivia, false, func(d *models.QuizDefinition) { d.Questions[0].Text = strings.Repeat("a", 501) }, "Question 1: text is over 500 characters"},
		{"bad kind", "poll", false, nil, "kind must be trivia or personality"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var def models.QuizDefinition
			if tc.kind == models.QuizKindPersonality {
				def = personality()
			} else {
				def = trivia(2)
			}
			if tc.mutate != nil {
				tc.mutate(&def)
			}
			errs, _ := ValidateQuizDefinition(tc.kind, def, tc.strict)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("expected no errors, got %v", errs)
				}
				return
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e, tc.wantErr) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want an error containing %q, got %v", tc.wantErr, errs)
			}
		})
	}
}

func TestValidateQuizDefinition_UnreachableResultIsAWarning(t *testing.T) {
	def := personality()
	def.Results = append(def.Results, models.QuizResult{Key: "fox", Title: "Fox"})
	errs, warnings := ValidateQuizDefinition(models.QuizKindPersonality, def, true)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors %v", errs)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"fox"`) {
		t.Fatalf("want one warning naming fox, got %v", warnings)
	}
}

// The unreachable-result warning is a publish rule (spec 4.3), so a draft
// save does not raise it.
func TestValidateQuizDefinition_UnreachableResultNotWarnedOnDraft(t *testing.T) {
	def := personality()
	def.Results = append(def.Results, models.QuizResult{Key: "fox", Title: "Fox"})
	_, warnings := ValidateQuizDefinition(models.QuizKindPersonality, def, false)
	if len(warnings) != 0 {
		t.Fatalf("want no warnings on a draft save, got %v", warnings)
	}
}

func TestDeriveQuizBandKeys(t *testing.T) {
	def := trivia(4)
	def.Results = []models.QuizResult{{Key: "stale", Min: ptr(0), Max: ptr(1)}, {Min: ptr(2), Max: ptr(4)}, {Key: "kept"}}
	DeriveQuizBandKeys(models.QuizKindTrivia, &def)
	got := []string{def.Results[0].Key, def.Results[1].Key, def.Results[2].Key}
	want := []string{"0-1", "2-4", "kept"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("want keys %v, got %v", want, got)
	}
}

func TestDeriveQuizBandKeys_LeavesPersonalityKeys(t *testing.T) {
	def := personality()
	DeriveQuizBandKeys(models.QuizKindPersonality, &def)
	if def.Results[0].Key != "owl" || def.Results[1].Key != "cat" {
		t.Fatalf("personality keys must not change, got %+v", def.Results)
	}
}

func TestQuizFingerprint(t *testing.T) {
	base := trivia(2)
	same := trivia(2)
	same.Questions[0].Text = "Different wording"
	same.Questions[0].Explanation = "New explanation"
	same.Results[0].Title = "Renamed"
	if QuizFingerprint(models.QuizKindTrivia, base) != QuizFingerprint(models.QuizKindTrivia, same) {
		t.Fatal("text-only edits must not change the fingerprint")
	}

	reordered := trivia(2)
	reordered.Questions[0].Answers[0], reordered.Questions[0].Answers[1] = reordered.Questions[0].Answers[1], reordered.Questions[0].Answers[0]
	if QuizFingerprint(models.QuizKindTrivia, base) == QuizFingerprint(models.QuizKindTrivia, reordered) {
		t.Fatal("moving the correct answer must change the fingerprint")
	}

	longer := trivia(3)
	if QuizFingerprint(models.QuizKindTrivia, base) == QuizFingerprint(models.QuizKindTrivia, longer) {
		t.Fatal("adding a question must change the fingerprint")
	}

	p := personality()
	q := personality()
	q.Questions[0].Answers[0].ResultKey = ptr("cat")
	if QuizFingerprint(models.QuizKindPersonality, p) == QuizFingerprint(models.QuizKindPersonality, q) {
		t.Fatal("changing a result key must change the fingerprint")
	}
}
