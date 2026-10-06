package routes

import (
	"os"
	"strings"
)

const defaultPublicSiteURL = "https://www.thetriangle.org"

// newsletterSiteURL is the public site origin newsletter links resolve
// against: locked article links become {origin}/article/{slug} at render time.
// PUBLIC_SITE_URL overrides it (dev/staging); the default matches the
// dashboard's VITE_PUBLIC_SITE_URL default.
func newsletterSiteURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_SITE_URL")), "/"); v != "" {
		return v
	}
	return defaultPublicSiteURL
}
