export async function readErrorMessage(
  response: Response,
  fallback = `Could not complete request (${response.status})`,
): Promise<string> {
  try {
    const body = await response.json() as { error?: unknown } | null
    const message = typeof body?.error === "string" ? body.error.trim() : ""
    if (response.status === 403 && message === "forbidden") return "You don't have permission to perform this action."
    return message || fallback
  } catch {
    return fallback
  }
}
