// Conversion between the markup Trix keeps in the editor and the markup we
// store on the article.
//
// These two live together because they are inverses and have to stay that way.
// Trix represents an image as <figure data-trix-attachment='{json}'> with the
// caption, alt text and alignment encoded in JSON data attributes: markup that
// means nothing outside a Trix editor. The public site renders article HTML
// directly and has no Trix stylesheet, so what we persist is plain semantic
// <figure>/<img>/<figcaption> using the same WordPress class names as the
// migrated corpus that already renders correctly there.
//
// articleHtmlToTrix runs on load, trixHtmlToArticle on change. Anything one adds
// the other has to understand, or an image loses its caption on a round trip.

import {
  audioToShortcode,
  parseAudioShortcode,
  parseMediaList,
  embedToShortcode,
  parseAudioUrl,
  parseEmbedUrl,
  shortcodeToAudio,
  shortcodeToEmbed,
  type Embed,
} from "../lib/embedUrl"

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

export const EMBED_CONTENT_TYPE = "application/x-triangle-embed"

const isAudioContentType = (contentType: string): boolean => contentType.startsWith("audio/")

const PROVIDER_LABEL: Record<string, string> = {
  youtube: "YouTube",
  soundcloud: "SoundCloud",
  spotify: "Spotify",
  tiktok: "TikTok",
  instagram: "Instagram",
}

/**
 * The editor-side audio rule. Everything Scalene checks except the origin:
 * the editor has no MEDIA_BASE_URL, and the URL only ever came from the
 * server's upload response or a library row. Passing the URL's own origin as
 * the base makes parseAudioUrl check scheme, userinfo, port, path prefix and
 * extension and skip the origin comparison.
 */
export const isAcceptableAudioUrl = (url: string): string | null => {
  let origin: string
  try {
    origin = new URL(url).origin
  } catch {
    return null
  }
  return parseAudioUrl(url, origin)
}

/** The origin of the src inside an [audio] shortcode, or "" when unparsable. */
const safeOrigin = (shortcodeText: string): string => {
  const m = /\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)')/i.exec(shortcodeText)
  try {
    return new URL(m?.[1] ?? m?.[2] ?? "").origin
  } catch {
    return ""
  }
}

/**
 * The static preview card. Built with DOM APIs so the URL is always text. The
 * URL is a link so the author can check it; the editor's link handler opens
 * it in a new tab. url has already passed parseEmbedUrl, so it is https on a
 * known host.
 */
export const buildEmbedCardHtml = (provider: string, url: string): string => {
  const card = document.createElement("div")
  card.className = "embed-card"
  const label = document.createElement("strong")
  label.textContent = `${PROVIDER_LABEL[provider] ?? provider} embed`
  const link = document.createElement("a")
  link.href = url
  link.textContent = url
  const note = document.createElement("div")
  note.textContent = "The player renders on the live site."
  card.append(label, link, note)
  return card.outerHTML
}

/** The editor card for an uploaded MP3. Trix shows an audio file as an empty chip otherwise. */
export const buildAudioCardHtml = (filename: string): string => {
  const card = document.createElement("div")
  card.className = "embed-card"
  const label = document.createElement("strong")
  label.textContent = "Audio"
  const name = document.createElement("div")
  name.textContent = filename
  const note = document.createElement("div")
  note.textContent = "The player renders on the live site."
  card.append(label, name, note)
  return card.outerHTML
}

const buildInvalidCardHtml = (): string => {
  const card = document.createElement("div")
  card.className = "embed-card embed-card--invalid"
  card.textContent = "Invalid embed. Remove this block and insert it again."
  return card.outerHTML
}

/** One wrapper element around exactly one shortcode, or a bare shortcode. */
const ORIGINAL_BLOCK_SHAPE = /^(?:<(p|div)(?:\s[^>]*)?>\s*\[[^\]<>]*\]\s*<\/\1>|\[[^\]<>]*\])$/i

// Exactly the wrapper attributes the legacy corpus carries. Anything else
// (an event handler, or a data-* hook the public site's scripts query) means
// the embedOriginal was not written by the load pass, so the canonical
// shortcode is emitted instead.
const isCorpusWrapperAttribute = (attr: Attr): boolean =>
  (attr.name === "class" && attr.value === "media-box") ||
  attr.name === "data-offset-key" ||
  (attr.name === "style" && /^text-align:\s*(left|center|right);?$/i.test(attr.value))

const buildEmbedFigure = (doc: Document, embed: Embed, original?: string): HTMLElement => {
  const data: AttachmentData = {
    contentType: EMBED_CONTENT_TYPE,
    embedProvider: embed.provider,
    embedUrl: embed.url,
    content: buildEmbedCardHtml(embed.provider, embed.url),
  }
  if (original) data.embedOriginal = original
  if (embed.media?.length) data.embedMedia = embed.media
  const figure = doc.createElement("figure")
  figure.setAttribute("data-trix-attachment", JSON.stringify(data))
  figure.setAttribute("data-trix-attributes", "{}")
  figure.innerHTML = data.content ?? ""
  return figure
}

const buildInvalidFigure = (doc: Document): HTMLElement => {
  const figure = doc.createElement("figure")
  const content = buildInvalidCardHtml()
  figure.setAttribute("data-trix-attachment", JSON.stringify({ contentType: EMBED_CONTENT_TYPE, content }))
  figure.setAttribute("data-trix-attributes", "{}")
  figure.innerHTML = content
  return figure
}

// The Trix caption on an audio chip is the "where this audio is from" line
// that Scalene prints above the player; it rides on the shortcode as source=.
const buildAudioFigure = (doc: Document, url: string, source = ""): HTMLElement => {
  const filename = url.split("/").pop() ?? "audio.mp3"
  const data: AttachmentData = {
    contentType: "audio/mpeg",
    url,
    href: url,
    filename,
    content: buildAudioCardHtml(filename),
  }
  const figure = doc.createElement("figure")
  figure.setAttribute("data-trix-attachment", JSON.stringify(data))
  figure.setAttribute("data-trix-attributes", JSON.stringify(source ? { caption: source } : {}))
  figure.innerHTML = data.content ?? ""
  return figure
}

/** Wrap a figure in the <div> Trix uses for an attachment block. */
const wrapBlock = (doc: Document, figure: HTMLElement): HTMLElement => {
  const block = doc.createElement("div")
  block.appendChild(figure)
  return block
}

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
  content?: string
  embedProvider?: string
  embedUrl?: string
  embedOriginal?: string
  embedMedia?: string[]
}

// The embed with its stored media list attached, when every path still
// passes the rule; a tampered list drops to the plain link.
const withMedia = (embed: Embed, media: unknown): Embed => {
  if (embed.provider !== "instagram" || !Array.isArray(media)) return embed
  const valid = parseMediaList(media.map(String).join(","))
  return valid.length > 0 ? { ...embed, media: valid } : embed
}

/** The library image an existing attachment is being replaced with. */
export type ReplacementImage = {
  url: string
  file_name: string
  mime_type?: string
  width?: number
  height?: number
  alt_text?: string
}

/**
 * The attachment attributes that point one image in the article at a
 * replacement, for Attachment#setAttributes.
 *
 * Only the image changes. The caption is a piece attribute, which this never
 * touches, so it stays where it is. The alt text was written for this spot in
 * this article, and a replacement is usually the same picture re-shot or
 * re-cropped, so it is kept too; the exception is a library image that comes
 * with a description of its own, which describes what is now actually there.
 * An upload never does: it is a brand-new asset.
 *
 * width and height are set explicitly, to undefined when unknown, because
 * setAttributes merges: the old image's dimensions would otherwise stick to
 * the new one. Left unset, Trix measures the new image when it loads.
 */
export const replacementAttachmentAttributes = (
  currentAlt: string,
  image: ReplacementImage,
  source: "library" | "upload",
): Partial<AttachmentData> => {
  const libraryAlt = source === "library" ? (image.alt_text?.trim() ?? "") : ""
  const alt = libraryAlt || currentAlt.trim()
  return {
    url: image.url,
    contentType: image.mime_type || contentTypeForUrl(image.url),
    filename: alt || image.file_name,
    alt,
    width: image.width || undefined,
    height: image.height || undefined,
    // Same reason as on insert: Trix's previewablePattern excludes
    // extension-less URLs.
    previewable: true,
  }
}

/**
 * The piece attributes Trix stores in data-trix-attributes. `align` is only
 * kept because TrixEditor.tsx adds it to AttachmentPiece.permittedAttributes;
 * Trix strips unrecognised piece attributes on its own.
 */
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

// An image can link somewhere (a newsletter's comic links to the comic). Trix
// keeps the link as the attachment's href. Only http(s) links survive, and not
// WordPress's "link to media file", which only opens the image itself.
const IMAGE_FILE = /\.(?:jpe?g|png|gif|webp|avif|svg|bmp|tiff?)(?:[?#]|$)/i

const safeLink = (href: string): string | null => {
  const trimmed = href.trim()
  try {
    const url = new URL(trimmed)
    if (url.protocol !== "http:" && url.protocol !== "https:") return null
  } catch {
    return null
  }
  return IMAGE_FILE.test(trimmed) ? null : trimmed
}

const imageLink = (img: Element): string | null => {
  const href = img.closest("a")?.getAttribute("href")
  return href ? safeLink(href) : null
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
 * text, so the caption "isn't picked up by the editor". It handles both the
 * markup this module writes and the two WordPress import shapes (block editor:
 * <figure><figcaption>; classic editor: <div class="wp-caption">…<*
 * class="wp-caption-text">).
 *
 * Idempotent: figures that already carry data-trix-attachment are skipped.
 */
export const articleHtmlToTrix = (html: string): string => {
  if (!["<figure", "wp-caption", "[", "data-trix-attachment"].some((marker) => html.includes(marker))) return html
  const doc = parseDocument(html)
  if (!doc) return html

  let changed = false

  // Pass 1: every stored embed or audio figure is rebuilt from its attributes.
  // Stored `content` is never rendered; a figure whose attributes do not
  // validate becomes an inert error card that the save guard refuses.
  // Any element, not only <figure>: Trix treats every [data-trix-attachment]
  // element as an attachment.
  for (const figure of Array.from(doc.querySelectorAll("[data-trix-attachment]"))) {
    const data = parseJSONAttribute<AttachmentData>(figure, "data-trix-attachment")
    const contentType = data?.contentType ?? ""
    if (contentType === EMBED_CONTENT_TYPE) {
      const parsed = parseEmbedUrl(data?.embedUrl ?? "")
      const valid = parsed.ok && parsed.embed.provider === data?.embedProvider
      figure.replaceWith(valid ? buildEmbedFigure(doc, withMedia(parsed.embed, data?.embedMedia), data?.embedOriginal) : buildInvalidFigure(doc))
      changed = true
    } else if (isAudioContentType(contentType)) {
      const url = isAcceptableAudioUrl(data?.url ?? "")
      const source = parseJSONAttribute<AttachmentAttributes>(figure, "data-trix-attributes")?.caption?.trim() ?? ""
      figure.replaceWith(url ? buildAudioFigure(doc, url, source) : buildInvalidFigure(doc))
      changed = true
    }
  }

  // Pass 2: a top-level <p> or <div> whose whole content is one shortcode
  // becomes a card. Legacy blocks keep their original markup in embedOriginal
  // so an unedited article saves back unchanged. Text inline with prose, bare
  // text nodes and anything that does not parse stay as they are.
  for (const block of Array.from(doc.body.children)) {
    const tag = block.tagName.toLowerCase()
    if (tag !== "p" && tag !== "div") continue
    if (block.children.length > 0) continue
    const text = (block.textContent ?? "").trim()
    if (!text.startsWith("[")) continue
    const embed = shortcodeToEmbed(text)
    if (embed) {
      // A block already in canonical form needs no original: save rebuilds it
      // byte-for-byte from the embed alone.
      const canonical = doc.createElement("div")
      canonical.textContent = embedToShortcode(embed)
      const original = block.outerHTML === canonical.outerHTML ? undefined : block.outerHTML
      block.replaceWith(wrapBlock(doc, buildEmbedFigure(doc, embed, original)))
      changed = true
      continue
    }
    const audio = parseAudioShortcode(text, safeOrigin(text))
    if (audio) {
      block.replaceWith(wrapBlock(doc, buildAudioFigure(doc, audio.src, audio.source)))
      changed = true
    }
  }

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
    const href = imageLink(img)
    if (href) data.href = href

    // Deliberately no presentation. Trix's "gallery" presentation fuses
    // adjacent images into a single block; see the config in TrixEditor.tsx.
    const attributes: AttachmentAttributes = {}
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

    // Give each image a block of its own. Trix deliberately does not count an
    // attachment element as a block (its isBlockElement bails on anything
    // carrying data-trix-attachment), so figures that are siblings in the
    // stored markup (the ordinary shape of the WordPress corpus) parse into
    // one shared block. A block is the unit the editor's reordering moves, so
    // that made two adjacent images inseparable: dragging either moved both,
    // and swapping them was not expressible at all. The <div> wrapper is how
    // Trix serializes an attachment block itself, so this round-trips cleanly.
    //
    // Only at the top level: a <div> nested inside a <p> would not survive
    // being re-parsed, and a figure already inside a block has one.
    if (container.parentElement === doc.body) {
      const block = doc.createElement("div")
      block.appendChild(figure)
      container.replaceWith(block)
    } else {
      container.replaceWith(figure)
    }
    changed = true
  }

  return changed ? doc.body.innerHTML : html
}

// ── Save: Trix attachment markup → article HTML ──────────────────────────────

/**
 * Trix wraps an attachment in its own <div> block. The public site only reads a
 * shortcode that is the whole content of a top-level block, so when the figure
 * is the only child of that <div>, replace the <div>. When it shares a block
 * with other content, move the shortcode out to a top-level sibling right after
 * that block; nested, it would render as literal text.
 */
const replaceAttachmentBlock = (figure: Element, replacement: Node): void => {
  const parent = figure.parentElement
  if (parent && parent.tagName === "DIV" && parent.childNodes.length === 1 && parent.parentElement?.tagName === "BODY") {
    parent.replaceWith(replacement)
    return
  }
  let top: Element = figure
  while (top.parentElement && top.parentElement.tagName !== "BODY") top = top.parentElement
  if (top === figure) {
    figure.replaceWith(replacement)
  } else {
    top.after(replacement)
    figure.remove()
    if (top.textContent?.trim() === "" && top.children.length === 0) top.remove()
  }
}

/**
 * The stored legacy block to emit for an unedited embed, or null. It is only
 * trusted when it is one allowed wrapper around one shortcode that resolves to
 * the same embed the attachment holds, so a crafted embedOriginal cannot carry
 * other markup or point somewhere else. It is parsed in the inert DOMParser
 * document, never the live page.
 */
const unchangedOriginalBlock = (doc: Document, original: string, embed: Embed): Node | null => {
  if (!ORIGINAL_BLOCK_SHAPE.test(original)) return null
  const holder = doc.createElement("div")
  holder.innerHTML = original
  const node = holder.firstChild
  if (!node || holder.childNodes.length !== 1) return null
  if (node.nodeType === Node.ELEMENT_NODE) {
    const wrapper = node as Element
    // The shortcode is compared on decoded text, so any markup inside the
    // brackets would pass that check and be written out verbatim.
    if (wrapper.children.length > 0) return null
    if (!Array.from(wrapper.attributes).every(isCorpusWrapperAttribute)) return null
  }
  const stored = shortcodeToEmbed(node.textContent ?? "")
  return stored && stored.provider === embed.provider && stored.url === embed.url ? node : null
}

/**
 * Rewrite Trix's attachment figures into the semantic markup we persist and the
 * public site renders: <figure class="wp-caption alignX"><img src alt><figcaption
 * class="wp-caption-text">.
 *
 * Non-image attachments are left exactly as Trix wrote them: this only knows
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

  // Reversed so two attachments in one block each land right after it and end
  // up in their original order.
  for (const figure of Array.from(doc.querySelectorAll("figure[data-trix-attachment]")).reverse()) {
    const data = parseJSONAttribute<AttachmentData>(figure, "data-trix-attachment")
    if (!data) continue
    // Before the url check: an embed attachment has no url of its own.
    if (data.contentType === EMBED_CONTENT_TYPE) {
      const parsed = parseEmbedUrl(data.embedUrl ?? "")
      if (!parsed.ok || parsed.embed.provider !== data.embedProvider) continue // invalid: left for findInvalidEmbeds
      const original = data.embedOriginal ? unchangedOriginalBlock(doc, data.embedOriginal, parsed.embed) : null
      const out = original ?? doc.createElement("div")
      if (!original) out.textContent = embedToShortcode(withMedia(parsed.embed, data.embedMedia))
      replaceAttachmentBlock(figure, out)
      changed = true
      continue
    }
    const src = data.url?.trim()
    if (!src) continue
    // A still-uploading attachment is on a local blob: URL that means nothing
    // once the page reloads. Leave it as Trix has it so the in-progress state
    // survives, rather than baking a dead URL into the saved article.
    if (src.startsWith("blob:") || src.startsWith("data:")) continue
    if (data.contentType && isAudioContentType(data.contentType)) {
      const url = isAcceptableAudioUrl(src)
      if (!url) continue // invalid: left for findInvalidEmbeds
      const out = doc.createElement("div")
      const source = parseJSONAttribute<AttachmentAttributes>(figure, "data-trix-attributes")?.caption?.trim() ?? ""
      out.textContent = audioToShortcode(url, source)
      replaceAttachmentBlock(figure, out)
      changed = true
      continue
    }
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
    // are presentational hints that CSS outranks, so the width would be
    // overridden while the height still applied, stretching every image. They
    // would normally be worth emitting to reserve layout space, but that only
    // holds on a page with a matching `height: auto`.
    img.setAttribute("loading", "lazy")
    const href = typeof data.href === "string" ? safeLink(data.href) : null
    if (href) {
      const a = doc.createElement("a")
      a.setAttribute("href", href)
      a.appendChild(img)
      out.appendChild(a)
    } else {
      out.appendChild(img)
    }

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

/**
 * Human messages for every embed or audio attachment that cannot be saved:
 * invalid attributes, or an upload that has not finished. Runs over the
 * converted article HTML, where such figures are passed through untouched. The
 * ordinal counts every embed and audio block in document order, including the
 * ones that already became shortcodes, so "Embed 3" is the third one the
 * author sees.
 */
export const findInvalidEmbeds = (articleHtml: string): string[] => {
  if (!articleHtml.includes("data-trix-attachment")) return []
  const doc = parseDocument(articleHtml)
  if (!doc) return []
  const messages: string[] = []
  let position = 0
  for (const el of Array.from(doc.querySelectorAll("[data-trix-attachment], body > p, body > div"))) {
    if (!el.hasAttribute("data-trix-attachment")) {
      // A valid embed or audio block is a bare shortcode by now.
      const text = (el.textContent ?? "").trim()
      if (el.children.length === 0 && text.startsWith("[") && (shortcodeToEmbed(text) || shortcodeToAudio(text, safeOrigin(text)))) {
        position += 1
      }
      continue
    }
    const data = parseJSONAttribute<AttachmentData>(el, "data-trix-attachment")
    const contentType = data?.contentType ?? ""
    const isAudio = isAudioContentType(contentType)
    if (contentType !== EMBED_CONTENT_TYPE && !isAudio) continue
    position += 1
    const label = isAudio ? "Audio" : "Embed"
    const url = data?.url ?? ""
    if (!isAudio) {
      messages.push(`${label} ${position} has an invalid URL. Remove the block and insert it again.`)
    } else if (url.startsWith("blob:") || url.startsWith("data:") || !url) {
      messages.push(`${label} ${position} has not finished uploading.`)
    } else {
      messages.push(`${label} ${position} has an invalid URL. Remove the block and upload it again.`)
    }
  }
  return messages
}
