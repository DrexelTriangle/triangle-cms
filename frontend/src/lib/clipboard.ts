/**
 * Copy text to the clipboard, returning whether it worked.
 *
 * navigator.clipboard exists only on secure origins. The CMS is still served
 * over plain HTTP on Delta (the host Nginx site lives in the
 * triangle-infrastructure repo), where the whole API is undefined -- so the
 * modern path alone would fail on exactly the
 * deployment editors use today. Fall back to the deprecated execCommand copy,
 * which has no such restriction, and only report failure if both fail.
 */
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Blocked by permissions policy or a denied prompt; try the fallback.
    }
  }

  try {
    const textarea = document.createElement("textarea")
    textarea.value = text
    // Keep it off-screen and non-scrolling so the page does not jump.
    textarea.setAttribute("readonly", "")
    textarea.style.position = "fixed"
    textarea.style.top = "-1000px"
    textarea.style.opacity = "0"
    document.body.appendChild(textarea)
    textarea.select()
    const copied = document.execCommand("copy")
    document.body.removeChild(textarea)
    return copied
  } catch {
    return false
  }
}
