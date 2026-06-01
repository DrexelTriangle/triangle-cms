function trimTrailingSlashes(value: string) {
  return value.replace(/\/+$/, "")
}

export function apiBaseUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_API_BASE_URL ?? "")
}

export function authBaseUrl() {
  return trimTrailingSlashes(import.meta.env.VITE_AUTH_BASE_URL ?? "https://localhost:8080")
}
