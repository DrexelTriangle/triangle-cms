package handlers

import (
	"encoding/json"
	"os"
	"testing"
)

func TestQuizShortcodeIDMatchesTheSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../frontend/src/lib/embed-vectors.json")
	if err != nil {
		t.Fatalf("read the shared vectors: %v", err)
	}
	var vectors struct {
		Quiz struct {
			Accept []struct {
				Input string `json:"input"`
				ID    int64  `json:"id"`
			} `json:"accept"`
			Reject []string `json:"reject"`
		} `json:"quiz"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Quiz.Accept) == 0 || len(vectors.Quiz.Reject) == 0 {
		t.Fatal("embed-vectors.json has no quiz vectors")
	}
	for _, v := range vectors.Quiz.Accept {
		if id, ok := quizShortcodeID(v.Input); !ok || id != v.ID {
			t.Errorf("%q: want id %d, got %d (ok=%v)", v.Input, v.ID, id, ok)
		}
	}
	for _, input := range vectors.Quiz.Reject {
		if id, ok := quizShortcodeID(input); ok {
			t.Errorf("%q: want rejected, got id %d", input, id)
		}
	}
}
