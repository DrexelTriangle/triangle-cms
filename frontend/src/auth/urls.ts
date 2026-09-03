function trimTrailingSlashes(value: string) {
  return value.replace(/\/+$/, "")
}

export function apiBaseUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_API_BASE_URL ?? "")
}

export function authBaseUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_AUTH_BASE_URL ?? "")
}

// Public-facing site origin, used for article permalinks (e.g. so Yoast can
// tell internal links from outbound ones) and for the "View Live" links.
// Defaults to www now that the CMS drives production: an unconfigured build
// pointing at dev would send editors to a copy of the site their readers never
// see. Override with VITE_PUBLIC_SITE_URL to aim a build at dev instead.
export function publicSiteUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_PUBLIC_SITE_URL ?? "https://www.thetriangle.org")
}

// The public permalink for a slug. The URL is fully determined by the slug, so
// it can be handed out (newsletter, social scheduling) before the article is
// published. It 404s until then, and resolves the moment it goes live.
export function articleUrl(slug: string) {
  return `${publicSiteUrl()}/article/${encodeURIComponent(slug)}`
}
