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
