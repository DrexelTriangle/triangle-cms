export function readSessionJSON<T>(key: string, fallback: T): T {
  try {
    const raw = window.sessionStorage.getItem(key)
    return raw ? JSON.parse(raw) as T : fallback
  } catch {
    return fallback
  }
}

export function writeSessionJSON(key: string, value: unknown): void {
  try {
    window.sessionStorage.setItem(key, JSON.stringify(value))
  } catch {
    // Caching is optional when storage is unavailable or full.
  }
}

export function clearArticleListCache(scope: "results" | "all" = "results"): void {
  try {
    const storage = window.sessionStorage
    for (let index = storage.length - 1; index >= 0; index--) {
      const key = storage.key(index)
      if (key?.startsWith("articleView:") && (scope === "all" || key.endsWith(":results"))) {
        storage.removeItem(key)
      }
    }
  } catch {
    // Storage failure must not turn a successful API write into a save error.
  }
}
