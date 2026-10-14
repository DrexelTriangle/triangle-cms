package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWordangleWordSendsPuzzleNumberZero(t *testing.T) {
	// Launch day is Wordangle #0; omitempty must not drop it.
	zero := 0
	body, err := json.Marshal(WordangleWord{Date: "2026-10-09", Number: &zero, Word: "crane"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"number":0`) {
		t.Fatalf("JSON = %s, want \"number\":0", body)
	}
}

func TestWordangleWordOmitsMissingPuzzleNumber(t *testing.T) {
	body, err := json.Marshal(WordangleWord{Word: "crane"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"number"`) {
		t.Fatalf("JSON = %s, want no number", body)
	}
}
