package handlers

import (
	"context"
	"testing"

	"server/internal/models"
)

func TestValidateTaxonomyParent_RequiresParentForSubsection(t *testing.T) {
	if _, err := validateTaxonomyParent(context.Background(), nil, string(models.TaxonomyTypeSubsection), nil); err == nil {
		t.Fatal("expected missing parent_slug to be rejected")
	}
}

func TestValidateTaxonomyParent_RejectsParentForSection(t *testing.T) {
	parent := "news"

	if _, err := validateTaxonomyParent(context.Background(), nil, string(models.TaxonomyTypeSection), &parent); err == nil {
		t.Fatal("expected section parent_slug to be rejected")
	}
}

func TestValidateTaxonomyParent_NormalizesSubsectionParent(t *testing.T) {
	parent := "columns"

	got, err := validateTaxonomyParent(context.Background(), nil, string(models.TaxonomyTypeSubsection), &parent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "columns" {
		t.Fatalf("parent = %v, want columns", got)
	}
}

func TestNormalizeCategoryAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want string
	}{
		{"nil becomes an empty array", nil, `[]`},
		{"empty stays an empty array", []string{}, `[]`},
		{"trims", []string{"  Arts & Entertainment  "}, `["Arts & Entertainment"]`},
		{"drops blanks", []string{"Movies", "", "   "}, `["Movies"]`},
		{"drops case-insensitive duplicates", []string{"Movies", "movies", "MOVIES"}, `["Movies"]`},
		{"keeps order", []string{"B", "A"}, `["B","A"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCategoryAliases(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("normalizeCategoryAliases(%v) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeCategoryAliasesNeverReturnsNull(t *testing.T) {
	// NULL means "never set" and is what the defaults seed on, so a write must
	// store [] instead -- otherwise clearing aliases would silently restore the
	// seeded defaults on the next startup.
	got, err := normalizeCategoryAliases(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "null" || got == "" {
		t.Fatalf("got %q, want an empty JSON array", got)
	}
}
