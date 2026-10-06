package newsletter

import (
	"fmt"
	"strings"
	"testing"
)

func TestParse_AcceptsEmptyAndValid(t *testing.T) {
	for _, raw := range []string{"", "null"} {
		doc, err := Parse([]byte(raw))
		if err != nil || doc.Version != 1 || len(doc.Blocks) != 0 {
			t.Errorf("Parse(%q) = %+v, %v; want empty v1 document", raw, doc, err)
		}
	}
	valid := `{"version":1,"blocks":[
		{"type":"text","html":"<p>Hi</p>"},
		{"type":"heading","text":"More From News"},
		{"type":"button","label":"Read","href":"https://www.thetriangle.org"},
		{"type":"button","label":"Locked","href":"cms-article:4"},
		{"type":"article","article_id":4,"show_image":false},
		{"type":"image","src":"https://cdn.example/a.jpg","alt":"A","href":"mailto:ads@thetriangle.org"},
		{"type":"divider"}]}`
	doc, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("valid document rejected: %v", err)
	}
	if len(doc.Blocks) != 7 || doc.Blocks[4].ArticleID != 4 || doc.Blocks[4].ShowImage == nil || *doc.Blocks[4].ShowImage {
		t.Errorf("parsed = %+v", doc)
	}
}

func TestParse_RejectsBadDocuments(t *testing.T) {
	block := func(s string) string { return `{"version":1,"blocks":[` + s + `]}` }
	many := make([]string, 101)
	for i := range many {
		many[i] = `{"type":"divider"}`
	}
	cases := map[string]string{
		"version 2":         `{"version":2,"blocks":[]}`,
		"unknown type":      block(`{"type":"script"}`),
		"unknown field":     block(`{"type":"divider","onclick":"x"}`),
		"101 blocks":        `{"version":1,"blocks":[` + strings.Join(many, ",") + `]}`,
		"long heading":      block(`{"type":"heading","text":"` + strings.Repeat("h", 201) + `"}`),
		"empty heading":     block(`{"type":"heading","text":"  "}`),
		"huge text":         block(`{"type":"text","html":"` + strings.Repeat("x", 20<<10+1) + `"}`),
		"javascript button": block(`{"type":"button","label":"x","href":"javascript:alert(1)"}`),
		"button no label":   block(`{"type":"button","href":"https://a.io"}`),
		"http image":        block(`{"type":"image","src":"http://cdn.example/a.jpg"}`),
		"image no src":      block(`{"type":"image","alt":"x"}`),
		"article no id":     block(`{"type":"article"}`),
		"negative id":       block(`{"type":"article","article_id":-1}`),
		"long url":          block(`{"type":"button","label":"x","href":"https://a.io/` + strings.Repeat("a", 2048) + `"}`),
		"not json":          `<p>hi</p>`,
		"trailing garbage":  `{"version":1,"blocks":[]} {}`,
		"oversized":         `{"version":1,"blocks":[],"pad":"` + strings.Repeat("x", 512<<10) + `"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw)); err == nil {
				t.Errorf("accepted: %s", truncate(raw))
			} else if _, ok := err.(*ValidationError); !ok {
				t.Errorf("error %T (%v) is not a *ValidationError", err, err)
			}
		})
	}
}

func truncate(s string) string {
	if len(s) > 80 {
		return fmt.Sprintf("%s… (%d bytes)", s[:80], len(s))
	}
	return s
}
