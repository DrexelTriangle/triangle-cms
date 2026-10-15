package handlers

import (
	"reflect"
	"testing"
)

func TestQuizIDsInContent(t *testing.T) {
	html := `<p>Intro</p><p>[quiz id="12"]</p><div>[quiz id="7" /]</div><p>[quiz id="12"]</p><p>[quiz id="x"]</p><p>[QUIZ id="3"]</p><p>[quiz id="0"]</p>`
	got := quizIDsInContent(html)
	if !reflect.DeepEqual(got, []int64{12, 7, 3}) {
		t.Fatalf("want [12 7 3] in document order without repeats or id 0, got %v", got)
	}
	if got := quizIDsInContent("<p>no quiz here</p>"); len(got) != 0 {
		t.Fatalf("want none, got %v", got)
	}
}
