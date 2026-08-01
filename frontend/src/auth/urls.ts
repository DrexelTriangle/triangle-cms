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
// Defaults to the dev site: the CMS is not yet driving production, so an
// unconfigured build pointing at www would send editors to pages that do not
// reflect what they just saved.
export function publicSiteUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_PUBLIC_SITE_URL ?? "https://dev.thetriangle.org")
}
