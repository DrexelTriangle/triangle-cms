package database

import "testing"

func TestParseDevelopingStoriesReadsLegacyTitleArray(t *testing.T) {
	stories := parseDevelopingStories(`["Title IX Coordinator departs suddenly", "  ", "Second story"]`)
	if len(stories) != 2 {
		t.Fatalf("got %d stories, want 2: %#v", len(stories), stories)
	}
	if stories[0].Title != "Title IX Coordinator departs suddenly" || stories[0].Description != "" {
		t.Fatalf("unexpected first story: %#v", stories[0])
	}
	if stories[1].Title != "Second story" {
		t.Fatalf("unexpected second story: %#v", stories[1])
	}
}

func TestParseDevelopingStoriesReadsObjectsAndDropsDuplicates(t *testing.T) {
	stories := parseDevelopingStories(`[
		{"title": " Title IX Coordinator departs suddenly ", "description": " Blaze Bowers departs the office. "},
		{"title": "Title IX Coordinator departs suddenly", "description": "ignored duplicate"},
		{"title": "", "description": "no title"}
	]`)
	if len(stories) != 1 {
		t.Fatalf("got %d stories, want 1: %#v", len(stories), stories)
	}
	if stories[0].Title != "Title IX Coordinator departs suddenly" {
		t.Fatalf("title not trimmed: %#v", stories[0])
	}
	if stories[0].Description != "Blaze Bowers departs the office." {
		t.Fatalf("description not trimmed: %#v", stories[0])
	}
}

func TestParseDevelopingStoriesToleratesJunk(t *testing.T) {
	for _, raw := range []string{"", "   ", "not json", `{"title":"object not array"}`} {
		if got := parseDevelopingStories(raw); len(got) != 0 {
			t.Fatalf("parseDevelopingStories(%q) = %#v, want empty", raw, got)
		}
	}
}
