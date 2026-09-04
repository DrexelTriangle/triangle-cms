package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeSectionSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: " sports ", want: "sports"},
		{in: "Opinion", want: "opinion"},
		{in: "", want: ""},
		{in: "sports", want: "sports"},
	}

	for _, tc := range cases {
		if got := normalizeSectionSlug(tc.in); got != tc.want {
			t.Fatalf("normalizeSectionSlug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// site_taxonomy is the only definition of a section, so with no handle to it
// nothing resolves. The section and subsection tree itself is exercised against
// a real database in taxonomy_integration_test.go; these cases only pin the
// no-taxonomy contract and the status-code mapping, which need no rows.
func TestNormalizeAndValidateArticleParams_RejectsUnknownSectionSlug(t *testing.T) {
	_, err := normalizeAndValidateArticleParams(context.Background(), nil, ArticleParams{
		Section: "sports",
	})
	if err == nil {
		t.Fatal("expected error for unknown section_slug")
	}
	if !errors.Is(err, errSectionNotFound) {
		t.Fatalf("err = %v, want errSectionNotFound", err)
	}
}

func TestNormalizeAndValidateArticleParams_RejectsUnknownSubsectionSlug(t *testing.T) {
	_, err := normalizeAndValidateArticleParams(context.Background(), nil, ArticleParams{
		Subsection: "not-a-subsection",
	})
	if !errors.Is(err, errSubsectionNotFound) {
		t.Fatalf("err = %v, want errSubsectionNotFound", err)
	}
}

func TestArticleParamsStatus_OnlyPathParamDegradesToNotFound(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		pathParamErr error
		want         int
	}{
		{name: "section in path", err: errSectionNotFound, pathParamErr: errSectionNotFound, want: http.StatusNotFound},
		{name: "subsection in path", err: errSubsectionNotFound, pathParamErr: errSubsectionNotFound, want: http.StatusNotFound},
		{name: "subsection is a query filter", err: errSubsectionNotFound, pathParamErr: errSectionNotFound, want: http.StatusBadRequest},
		{name: "section is a query filter", err: errSectionNotFound, pathParamErr: errSubsectionNotFound, want: http.StatusBadRequest},
		{name: "malformed author_slug", err: errors.New("invalid author_slug"), pathParamErr: errSectionNotFound, want: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := articleParamsStatus(tc.err, tc.pathParamErr); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// With no database handle no slug resolves, so the handlers answer before
// anything is queried.
func TestSectionArticlesHandlers_UnknownPathSlugReturnsNotFound(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		target  string
		pathKey string
		slug    string
	}{
		{
			name:    "section",
			handler: GetSectionArticles(nil),
			target:  "/v1/sections/sw.js/articles",
			pathKey: "section_slug",
			slug:    "sw.js",
		},
		{
			name:    "subsection",
			handler: GetSubsectionArticles(nil),
			target:  "/v1/subsections/sw.js/articles",
			pathKey: "subsection_slug",
			slug:    "sw.js",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.SetPathValue(tc.pathKey, tc.slug)
			rec := httptest.NewRecorder()

			tc.handler(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, http.StatusNotFound, rec.Body.String())
			}
		})
	}
}
