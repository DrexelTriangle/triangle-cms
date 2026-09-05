export async function readErrorMessage(
  response: Response,
  fallback = `Could not complete request (${response.status})`,
): Promise<string> {
  try {
    const body = await response.json() as { error?: unknown } | null
    return typeof body?.error === "string" ? body.error.trim() || fallback : fallback
  } catch {
    return fallback
  }
}
