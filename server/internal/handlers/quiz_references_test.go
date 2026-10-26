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

// Only a block the site would render as a quiz holds up publishing. A
// shortcode quoted in prose or a code sample stays text on the site.
func TestQuizIDsInContentOnlyWholeBlocks(t *testing.T) {
	for _, html := range []string{
		`<p>Embed one with [quiz id="9"] on a line of its own.</p>`,
		`<p>[quiz id="9"] trailing</p>`,
		`<pre>[quiz id="9"]</pre>`,
		`<p><code>[quiz id="9"]</code></p>`,
		`[quiz id="9"]`,
		`<p><!-- note -->[quiz id="9"]</p>`,
	} {
		if got := quizIDsInContent(html); len(got) != 0 {
			t.Errorf("%s: want none, got %v", html, got)
		}
	}

	// Every shape Scalene expands must still be caught, including ones a
	// stricter parser would skip.
	for _, html := range []string{
		`<div>[quiz id="9"]</div>`,
		`<P class="x">  [quiz id="9"]  </P>`,
		"<div data-a=\"a>b\">\n[quiz id=\"9\" /]\n</div>",
		"<p>\u00a0\ufeff[quiz id=\"9\"]\u3000</p>",
		`<p>[quiz id="9"]</div>`,
		`<blockquote><div>[quiz id="9"]</div></blockquote>`,
	} {
		if got := quizIDsInContent(html); !reflect.DeepEqual(got, []int64{9}) {
			t.Errorf("%s: want [9], got %v", html, got)
		}
	}
}
