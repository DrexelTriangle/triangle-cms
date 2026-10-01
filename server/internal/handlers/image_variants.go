package handlers

import "server/internal/models"

// ImageVariantLookup finds the resized renditions of a library image. It is
// satisfied by *imaging.Index; an interface so this package does not depend on
// how renditions are made.
type ImageVariantLookup interface {
	ForURL(imageURL string) []models.ImageVariant
	ForPath(mediaPath string) []models.ImageVariant
	ForContent(body string) map[string][]models.ImageVariant
}

// imageVariants is package state rather than a handler argument because
// article list items are assembled in helpers several calls away from any
// handler constructor. It is set once at startup, before the server listens.
var imageVariants variantLookup

// SetImageVariants installs the rendition lookup. Without it, responses simply
// carry no variants and clients fall back to the original image.
func SetImageVariants(lookup ImageVariantLookup) {
	imageVariants = variantLookup{lookup: lookup}
}

// variantLookup makes an unset lookup answer "no variants" instead of
// panicking, which is what every test and every deployment without the
// sidecar relies on.
type variantLookup struct {
	lookup ImageVariantLookup
}

func (v variantLookup) ForURL(imageURL string) []models.ImageVariant {
	if v.lookup == nil {
		return nil
	}
	return v.lookup.ForURL(imageURL)
}

func (v variantLookup) ForPath(mediaPath string) []models.ImageVariant {
	if v.lookup == nil {
		return nil
	}
	return v.lookup.ForPath(mediaPath)
}

func (v variantLookup) ForContent(body string) map[string][]models.ImageVariant {
	if v.lookup == nil {
		return nil
	}
	return v.lookup.ForContent(body)
}
