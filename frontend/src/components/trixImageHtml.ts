// Conversion between the markup Trix keeps in the editor and the markup we
// store on the article.
//
// These two live together because they are inverses and have to stay that way.
// Trix represents an image as <figure data-trix-attachment='{json}'> with the
// caption, alt text and alignment encoded in JSON data attributes -- markup that
// means nothing outside a Trix editor. The public site renders article HTML
// directly and has no Trix stylesheet, so what we persist is plain semantic
// <figure>/<img>/<figcaption> using the same WordPress class names as the
// migrated corpus that already renders correctly there.
//
// articleHtmlToTrix runs on load, trixHtmlToArticle on change. Anything one adds
// the other has to understand, or an image loses its caption on a round trip.

export const ALIGNMENTS = ["left", "center", "right"] as const
export type Alignment = (typeof ALIGNMENTS)[number]

// The WordPress alignment vocabulary, which the legacy corpus already uses.
const WP_ALIGN_CLASS: Record<Alignment, string> = {
  left: "alignleft",
  center: "aligncenter",
  right: "alignright",
}

// Trix has no alignment concept, so in the editor it is a class our overlay
// applies and our CSS styles.
export const TRIX_ALIGN_CLASS: Record<Alignment, string> = {
  left: "attachment--align-left",
  center: "attachment--align-center",
  right: "attachment--align-right",
}

const IMAGE_CONTENT_TYPES: Record<string, string> = {
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  png: "image/png",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  svg: "image/svg+xml",
}

export const contentTypeForUrl = (url: string): string => {
  const ext = url.split(/[?#]/)[0].split(".").pop()?.toLowerCase() ?? ""
  return IMAGE_CONTENT_TYPES[ext] ?? "image/jpeg"
}

// Trix's own parser labels an <img> it picked up with the bare type "image"
// rather than a full MIME type (see processElement in trix.esm.js), and that is
// exactly what a pasted image arrives as. Treating only "image/*" as an image
// would leave every pasted image unserialized.
const isImageContentType = (contentType: string): boolean =>
  contentType === "image" || contentType.startsWith("image/")

/** The attachment JSON Trix stores in data-trix-attachment. */
type AttachmentData = {
  contentType?: string
  url?: string
  href?: string
  filename?: string
  alt?: string
  width?: number
  height?: number
  previewable?: boolean
}

/** The piece attributes Trix stores in data-trix-attributes. */
type AttachmentAttributes = {
  caption?: string
  presentation?: string
  align?: string
}

const parseJSONAttribute = <T,>(el: Element, name: string): T | null => {
  const raw = el.getAttribute(name)
  if (!raw) return null
  try {
    const parsed: unknown = JSON.parse(raw)
    return parsed && typeof parsed === "object" ? (parsed as T) : null
  } catch {
    return null
  }
}

const alignmentFromClasses = (el: Element, table: Record<Alignment, string>): Alignment | null => {
  for (const alignment of ALIGNMENTS) {
    if (el.classList.contains(table[alignment])) return alignment
  }
  return null
}

const asAlignment = (value: unknown): Alignment | null =>
  ALIGNMENTS.includes(value as Alignment) ? (value as Alignment) : null

const positiveInt = (value: unknown): number | null => {
  const n = typeof value === "string" ? Number.parseInt(value, 10) : typeof value === "number" ? value : NaN
  return Number.isFinite(n) && n > 0 ? Math.round(n) : null
}

const parseDocument = (html: string): Document | null => {
  if (typeof window === "undefined" || !window.DOMParser) return null
  return new DOMParser().parseFromString(html, "text/html")
}

// ── Load: article HTML → Trix attachment markup ──────────────────────────────

/**
 * Rewrite stored image figures into Trix's native attachment format so the
 * editor keeps the caption bound to the image, exposes the alt text for
 * editing, and treats the figure as a movable attachment.
 *
 * Without this, Trix's parser drops a <figcaption> onto the next line as body
 * text -- the caption "isn't picked up by the editor". It handles both the
 * markup this module writes and the two WordPress import shapes (block editor:
 * <figure><figcaption>; classic editor: <div class="wp-caption">…<*
 * class="wp-caption-text">).
 *
 * Idempotent: figures that already carry data-trix-attachment are skipped.
 */
export const articleHtmlToTrix = (html: string): string => {
  if (!html.includes("<figure") && !html.includes("wp-caption")) return html
  const doc = parseDocument(html)
  if (!doc) return html

  let changed = false

  for (const container of Array.from(doc.querySelectorAll("figure, .wp-caption"))) {
    if (container.hasAttribute("data-trix-attachment")) continue
    // Only the simple single-image case is safe to rebuild. Galleries and
    // nested captioned figures hold more than one image (or another caption
    // container); reconstructing them from a single <img> would silently drop
    // the rest, so leave those untouched.
    if (container.querySelectorAll("img").length !== 1) continue
    if (container.querySelector("figure, .wp-caption")) continue

    const img = container.querySelector("img")
    const src = img?.getAttribute("src")?.trim()
    if (!img || !src) continue

    const caption = container.querySelector("figcaption, .wp-caption-text")?.textContent?.trim() ?? ""
    const alt = img.getAttribute("alt")?.trim() ?? ""
    const width = positiveInt(img.getAttribute("width"))
    const height = positiveInt(img.getAttribute("height"))
    // WordPress puts the alignment on either the container or the image itself.
    const align =
      alignmentFromClasses(container, WP_ALIGN_CLASS) ?? alignmentFromClasses(img, WP_ALIGN_CLASS)

    const data: AttachmentData = {
      contentType: contentTypeForUrl(src),
      url: src,
      // Trix shows filename in the caption placeholder; alt is the most useful
      // thing to surface there, with the basename as a fallback.
      filename: alt || src.split("/").pop()?.split(/[?#]/)[0] || "image",
      alt,
      // Force inline preview: Trix's previewablePattern excludes svg/avif and
      // any extension-less CDN URL, which would otherwise render as a file stub.
      previewable: true,
    }
    if (width) data.width = width
    if (height) data.height = height

    const attributes: AttachmentAttributes = { presentation: "gallery" }
    if (caption) attributes.caption = caption
    if (align) attributes.align = align

    const figure = doc.createElement("figure")
    figure.setAttribute("data-trix-attachment", JSON.stringify(data))
    figure.setAttribute("data-trix-attributes", JSON.stringify(attributes))
    if (align) figure.classList.add(TRIX_ALIGN_CLASS[align])

    const newImg = doc.createElement("img")
    newImg.setAttribute("src", src)
    if (alt) newImg.setAttribute("alt", alt)
    figure.appendChild(newImg)

    container.replaceWith(figure)
    changed = true
  }

  return changed ? doc.body.innerHTML : html
}

// ── Save: Trix attachment markup → article HTML ──────────────────────────────

/**
 * Rewrite Trix's attachment figures into the semantic markup we persist and the
 * public site renders: <figure class="wp-caption alignX"><img src alt><figcaption
 * class="wp-caption-text">.
 *
 * Non-image attachments are left exactly as Trix wrote them -- this only knows
 * how to reduce an image to an <img>, and silently mangling anything else would
 * lose data.
 *
 * All values are written through DOM setAttribute/textContent, so attacker- or
 * author-supplied captions and URLs are escaped by the serializer rather than by
 * hand-built string concatenation.
 */
export const trixHtmlToArticle = (html: string): string => {
  if (!html.includes("data-trix-attachment")) return html
  const doc = parseDocument(html)
  if (!doc) return html

  let changed = false

  for (const figure of Array.from(doc.querySelectorAll("figure[data-trix-attachment]"))) {
    const data = parseJSONAttribute<AttachmentData>(figure, "data-trix-attachment")
    const src = data?.url?.trim()
    if (!data || !src) continue
    // A still-uploading attachment is on a local blob: URL that means nothing
    // once the page reloads. Leave it as Trix has it so the in-progress state
    // survives, rather than baking a dead URL into the saved article.
    if (src.startsWith("blob:") || src.startsWith("data:")) continue
    if (data.contentType && !isImageContentType(data.contentType)) continue

    const attributes = parseJSONAttribute<AttachmentAttributes>(figure, "data-trix-attributes")
    const caption = attributes?.caption?.trim() ?? ""
    // Alignment may be a piece attribute (what survives a Trix round trip) or,
    // for a figure the overlay just moved, only a class on the live element.
    const align = asAlignment(attributes?.align) ?? alignmentFromClasses(figure, TRIX_ALIGN_CLASS)

    // Prefer the attachment's own alt. The inner <img> is Trix-rendered and its
    // alt may be a filename Trix filled in rather than authored alt text.
    const alt = (data.alt ?? figure.querySelector("img")?.getAttribute("alt") ?? "").trim()

    const out = doc.createElement("figure")
    out.classList.add("wp-caption")
    if (align) out.classList.add(WP_ALIGN_CLASS[align])

    const img = doc.createElement("img")
    img.setAttribute("src", src)
    // Always emit alt, even empty: an <img> with no alt attribute at all is an
    // accessibility failure, whereas alt="" is a valid "decorative" signal.
    img.setAttribute("alt", alt)
    // Deliberately no width/height. The public site sizes article images purely
    // in CSS (#article figure img { width: 100% }), and width/height attributes
    // are presentational hints that CSS outranks -- so the width would be
    // overridden while the height still applied, stretching every image. They
    // would normally be worth emitting to reserve layout space, but that only
    // holds on a page with a matching `height: auto`.
    img.setAttribute("loading", "lazy")
    out.appendChild(img)

    if (caption) {
      const figcaption = doc.createElement("figcaption")
      figcaption.className = "wp-caption-text"
      figcaption.textContent = caption
      out.appendChild(figcaption)
    }

    figure.replaceWith(out)
    changed = true
  }

  return changed ? doc.body.innerHTML : html
}
