package handlers

import (
	"fmt"
	"strings"
)

var allowedSubsectionsBySection = map[string]map[string]struct{}{
	"news": {
		"academic-transformation": {},
		"politics":                {},
		"transit":                 {},
		"crime-policy-violations": {},
	},
	"sports": {
		"mens-basketball":   {},
		"womens-basketball": {},
		"big-5":             {},
		"philly-sports":     {},
		"field-hockey":      {},
		"mens-soccer":       {},
		"womens-soccer":     {},
	},
	"opinion": {
		"science-tech":    {},
		"from-the-editor": {},
	},
	"columns": {
		"the-love-triangle":    {},
		"tri-this-sweet-treat": {},
	},
	"entertainment": {
		"movies":              {},
		"music":               {},
		"happening-in-philly": {},
		"cooking":             {},
		"books":               {},
		"gaming":              {},
		"listicles":           {},
	},
	"comics-puzzles": {
		"political-cartoons": {},
		"crossword":          {},
		"sudoku":             {},
	},
}

func normalizeAndValidateArticleParams(params ArticleParams) (ArticleParams, error) {
	params.AuthorSlug = strings.TrimSpace(params.AuthorSlug)
	params.Section = strings.ToLower(strings.TrimSpace(params.Section))
	params.Subsection = strings.ToLower(strings.TrimSpace(params.Subsection))

	if params.Section != "" {
		if _, ok := allowedSubsectionsBySection[params.Section]; !ok {
			return ArticleParams{}, fmt.Errorf("invalid section_slug")
		}
	}

	if params.Subsection != "" {
		parentSection, ok := sectionForSubsection(params.Subsection)
		if !ok {
			return ArticleParams{}, fmt.Errorf("invalid subsection_slug")
		}
		if params.Section == "" {
			params.Section = parentSection
		} else if params.Section != parentSection {
			return ArticleParams{}, fmt.Errorf("subsection_slug does not belong to section_slug")
		}
	}

	return params, nil
}

func sectionForSubsection(subsection string) (string, bool) {
	for section, subsections := range allowedSubsectionsBySection {
		if _, ok := subsections[subsection]; ok {
			return section, true
		}
	}
	return "", false
}
