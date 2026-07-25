function trimTrailingSlashes(value: string) {
  return value.replace(/\/+$/, "")
}

export function apiBaseUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_API_BASE_URL ?? "")
}

export function authBaseUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_AUTH_BASE_URL ?? "")
}

// Public-facing site origin, used to build article permalinks (e.g. so Yoast can
// tell internal links from outbound ones).
export function publicSiteUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_PUBLIC_SITE_URL ?? "https://www.thetriangle.org")
}
