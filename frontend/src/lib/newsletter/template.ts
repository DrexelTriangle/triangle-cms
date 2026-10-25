import { articleUrl, publicSiteUrl } from "../../auth/urls"
import type { NewsletterInput, NewsletterSponsor } from "./types"

// "To The Point", The Triangle's emailed digest. The fixed parts (sponsor
// banner, masthead, footer) live here; everything between the masthead and the
// footer is one document edited in the same Trix editor as articles, seeded by
// buildTemplateBody and turned into email HTML by renderNewsletterEmail.
//
// The editor has no email-specific blocks, so a few ordinary Trix formats carry
// the template's structure. The editor's formatting guide lists them:
//   Heading            -> article headline, with a divider above it
//   Quote              -> centred section label
//   A line that is only a link -> a button: a small one for a link to an
//                         article, a wide centred one (with a divider above,
//                         once past the intro) for anything else
//
// The look follows the issues WordPress's Newsletter plugin sent: image
// masthead, white page, #256f9c buttons, #0092ff dividers.

export const NEWSLETTER_NAME = "To The Point"
export const BUTTON_BLUE = "#256f9c"
const DIVIDER_BLUE = "#0092ff"
const TEXT = "#222222"
const EMAIL_WIDTH = 600
const SIDE_PADDING = 15

export const READ_MORE_LABEL = "Click Here to Read More"
export const READ_LATEST_LABEL = "Read the latest from The Triangle"
export const RANDOM_ARTICLE_LABEL = "Check out a Random Article"

// The masthead (unnamed.png) and social icons live in the CMS media tree on
// CephFS, served publicly by Delta: an image in a sent email has to load from a
// public URL for as long as the email sits in an inbox, whichever build sent
// it (a sandbox's public site is not reachable from Gmail). The directory is
// versioned because Delta serves /wp-content/ as immutable for 30 days: to
// change an image, add v2/ rather than overwrite.
export const EMAIL_ASSET_BASE = "https://delta.thetriangle.org/wp-content/email/v1"
const emailAsset = (name: string) => `${EMAIL_ASSET_BASE}/${name}`

export const FOOTER_TAGLINE = "The Independent Student Newspaper at Drexel University"
export const SOCIAL_LINKS = [
  { label: "X", url: "https://x.com/DrexelTriangle", icon: "x.png" },
  { label: "Instagram", url: "https://www.instagram.com/drexeltriangle/", icon: "instagram.png" },
] as const

// Left in the stored email for the sender to fill per recipient. Generic
// tokens rather than one provider's merge tags; see docs/newsletter-api.md.
export const EMAIL_TOKENS = {
  unsubscribe: "{{unsubscribe_url}}",
  manage: "{{manage_subscription_url}}",
  viewOnline: "{{view_online_url}}",
} as const

export type TemplateArticle = {
  title: string
  slug: string
  excerpt: string
  image_url?: string
  image_alt?: string
}

export type TemplateComic = {
  title: string
  slug: string
  image_url: string
  image_alt?: string
}

export type TemplateOptions = {
  articles: TemplateArticle[]
  comic?: TemplateComic | null
  question?: string
}

const escapeHtml = (value: string) =>
  value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;")

// CMS titles and excerpts arrive with a mix of raw characters and WordPress
// entities (&#8217;, &amp;). Decode once so escaping doesn't print them.
export const decodeEntities = (value: string): string => {
  if (!value.includes("&") || typeof DOMParser === "undefined") return value
  const doc = new DOMParser().parseFromString(`<!doctype html><body>${value}`, "text/html")
  return doc.body.textContent ?? value
}

const text = (value: string) => escapeHtml(decodeEntities(value).trim())

export function defaultSubject(date: Date): string {
  const formatted = date.toLocaleDateString("en-US", { month: "long", day: "numeric", year: "numeric" })
  return `${NEWSLETTER_NAME} — ${formatted}`
}

// The link rides through the editor as the image's href (trixImageHtml), so a
// story's photo opens the story and the comic opens the comic.
const imageHtml = (url: string | undefined, alt: string | undefined, href: string) =>
  url
    ? `<figure><a href="${escapeHtml(href)}"><img src="${escapeHtml(url)}" alt="${text(alt ?? "")}"></a></figure>`
    : ""

// One story: headline, lead image, excerpt and the read-more button. Plain
// article HTML, so every part stays editable once it is in the editor.
export function articleCardHtml(article: TemplateArticle): string {
  const excerpt = text(article.excerpt)
  return [
    `<h1>${text(article.title)}</h1>`,
    imageHtml(article.image_url, article.image_alt || decodeEntities(article.title), articleUrl(article.slug)),
    excerpt ? `<p>${excerpt}</p>` : "",
    `<p><a href="${escapeHtml(articleUrl(article.slug))}">${READ_MORE_LABEL}</a></p>`,
  ].join("")
}

export const INTRO_PLACEHOLDER =
  "It is Week [N], which means it is time for our [Nth] edition of the quarter. " +
  "Visit our website to read the most recent articles contributed by our wonderful staff. " +
  "We have news about [topics], and much more. " +
  "Make sure you read to stay up to date on all of your Drexel-related information!"

export function buildTemplateBody({ articles, comic, question }: TemplateOptions): string {
  const parts = [
    `<p>${escapeHtml(INTRO_PLACEHOLDER)}</p>`,
    `<p><a href="${escapeHtml(publicSiteUrl())}">${READ_LATEST_LABEL}</a></p>`,
  ]
  parts.push(...articles.map(articleCardHtml))
  parts.push(
    `<p><strong>Question of the day:</strong> ${
      question?.trim() ? text(question) : "[Your question here]"
    } (Reply to this email with your response!)</p>`,
  )
  // The site's /random page, the same as the Random Article link in its header.
  parts.push(`<p><a href="${escapeHtml(`${publicSiteUrl()}/random`)}">${RANDOM_ARTICLE_LABEL}</a></p>`)
  if (comic) {
    parts.push(imageHtml(comic.image_url, comic.image_alt || decodeEntities(comic.title), articleUrl(comic.slug)))
  }
  return parts.join("")
}

// ── Email rendering ──────────────────────────────────────────────────────────

const style = (el: Element, css: string) => el.setAttribute("style", css)

const FONT = "font-family:Helvetica,Arial,sans-serif;"
const BUTTON_FONT = "font-family:Verdana,Geneva,sans-serif;"

// An element that holds nothing but one link (and stray <br>/whitespace).
const soleLink = (el: Element): HTMLAnchorElement | null => {
  const meaningful = Array.from(el.childNodes).filter(
    (node) =>
      !(node.nodeType === Node.TEXT_NODE && !node.textContent?.trim()) &&
      !(node.nodeType === Node.ELEMENT_NODE && (node as Element).tagName === "BR"),
  )
  if (meaningful.length !== 1) return null
  const only = meaningful[0]
  if (only.nodeType !== Node.ELEMENT_NODE) return null
  const anchor = only as Element
  if (anchor.tagName === "A" && anchor.getAttribute("href")) return anchor as HTMLAnchorElement
  return null
}

// Desktop Outlook renders with Word, which ignores margin on <div> and
// <table> and padding on inline elements, but honours padding on a table cell
// and margin on <p>. So spacing around blocks comes from a cell's padding, and
// text blocks become <p>.
const spacer = (doc: Document, padding: string, ...content: Node[]): HTMLTableElement => {
  const table = doc.createElement("table")
  table.setAttribute("role", "presentation")
  table.setAttribute("width", "100%")
  table.setAttribute("cellpadding", "0")
  table.setAttribute("cellspacing", "0")
  const cell = table.insertRow().insertCell()
  style(cell, `padding:${padding};`)
  cell.append(...content)
  return table
}

// A link to an article gets the small read-more button under its story;
// anything else (the site, the random article) is a section button.
const isArticleLink = (link: HTMLAnchorElement) => /\/article\//.test(link.getAttribute("href") ?? "")

// A table, not a styled <a>: Outlook ignores padding on inline elements, so
// the padding is on the cell. A section button is 400px wide and centred.
const buttonFor = (doc: Document, link: HTMLAnchorElement, wide: boolean): HTMLTableElement => {
  const button = doc.createElement("table")
  button.setAttribute("role", "presentation")
  button.setAttribute("cellpadding", "0")
  button.setAttribute("cellspacing", "0")
  if (wide) {
    button.setAttribute("align", "center")
    button.setAttribute("width", "400")
    style(button, "width:100%;max-width:400px;")
  }
  const cell = button.insertRow().insertCell()
  cell.setAttribute("bgcolor", BUTTON_BLUE)
  if (wide) cell.setAttribute("align", "center")
  style(cell, `border-radius:5px;padding:10px 25px;background:${BUTTON_BLUE};`)
  const a = doc.createElement("a")
  a.setAttribute("href", link.getAttribute("href") ?? "#")
  a.textContent = link.textContent?.trim() || READ_MORE_LABEL
  style(a, `${BUTTON_FONT}font-size:16px;font-weight:normal;line-height:120%;color:#ffffff;text-decoration:none;`)
  cell.appendChild(a)
  return spacer(doc, wide ? "20px 0 30px" : "4px 0 30px", button)
}

const divider = (doc: Document) => {
  const rule = doc.createElement("div")
  style(rule, `border-top:3px solid ${DIVIDER_BLUE};font-size:0;line-height:0;`)
  rule.innerHTML = "&nbsp;"
  return spacer(doc, "20px 5px 24px", rule)
}

const BLOCKS = "div, p, h1, h2, h3, h4, ul, ol, blockquote, table, figure"

// Every image links somewhere: Gmail offers an unlinked image as a download,
// and readers expect a story's photo to open the story. The template links its
// own images (the comic to the comic); for one an author added without a
// link, a story's image takes the story's read-more link (the first article
// button after it, before the next headline), and any other the front page.
const linkImages = (doc: Document, root: HTMLElement) => {
  let pending: HTMLImageElement[] = []
  const settle = (href: string) => {
    for (const img of pending) {
      const a = doc.createElement("a")
      a.setAttribute("href", href)
      style(a, "display:block;text-decoration:none;")
      img.replaceWith(a)
      a.appendChild(img)
    }
    pending = []
  }
  for (const el of Array.from(root.querySelectorAll("img, a, h1"))) {
    if (el.tagName === "IMG") {
      if (!el.closest("a")) pending.push(el as HTMLImageElement)
    } else if (el.tagName === "H1") {
      settle(publicSiteUrl())
    } else if (el.parentElement && soleLink(el.parentElement) === el && isArticleLink(el as HTMLAnchorElement)) {
      settle(el.getAttribute("href") ?? publicSiteUrl())
    }
  }
  settle(publicSiteUrl())
}

const renamed = (el: Element, tag: string): Element => {
  const next = el.ownerDocument.createElement(tag)
  for (const attr of Array.from(el.attributes)) next.setAttribute(attr.name, attr.value)
  next.append(...Array.from(el.childNodes))
  el.replaceWith(next)
  return next
}

// Inline-style the editor's HTML for mail clients, which ignore <style> blocks
// as often as not.
export function bodyToEmailHtml(bodyHtml: string): string {
  const doc = new DOMParser().parseFromString(`<!doctype html><body>${bodyHtml}`, "text/html")
  const root = doc.body
  // Before the buttons replace the read-more links it looks for.
  linkImages(doc, root)

  // Document order matters here: text before the first headline is the
  // intro, set larger, and a section button only gets a divider once the
  // stories have started.
  let seenHeadline = false
  for (const el of Array.from(root.querySelectorAll("p, div, h1"))) {
    if (!el.isConnected || el.closest("figure")) continue
    if (el.tagName === "H1") {
      el.before(divider(doc))
      seenHeadline = true
      style(el, `${FONT}margin:0 0 16px;font-size:32px;line-height:1.3;font-weight:normal;color:${TEXT};`)
      continue
    }
    const link = soleLink(el)
    if (link) {
      const wide = !isArticleLink(link)
      const button = buttonFor(doc, link, wide)
      el.replaceWith(button)
      if (wide && seenHeadline) button.before(divider(doc))
      continue
    }
    if (el.querySelector("figure, img")) continue
    // Only a leaf block can become <p>; a <p> can't hold other blocks.
    const text = el.tagName === "DIV" && !el.querySelector(BLOCKS) ? renamed(el, "p") : el
    const size = seenHeadline ? "16px" : "18px"
    style(text, `${FONT}margin:0 0 15px;font-size:${size};line-height:1.5;color:${TEXT};`)
  }

  for (const quote of Array.from(root.querySelectorAll("blockquote"))) {
    style(quote, `${FONT}margin:24px 0 20px;padding:0;text-align:center;font-size:16px;color:${TEXT};`)
  }

  for (const img of Array.from(root.querySelectorAll("img"))) {
    img.removeAttribute("loading")
    img.setAttribute("width", String(EMAIL_WIDTH - 2 * SIDE_PADDING))
    style(img, "display:block;width:100%;max-width:100%;height:auto;border:0;")
  }
  for (const caption of Array.from(root.querySelectorAll("figcaption"))) {
    style(renamed(caption, "div"), `${FONT}padding:6px 0 0;font-size:12px;line-height:1.4;color:#57606a;`)
  }
  // Outlook, GMX and WEB.DE don't know <figure>; a spacer cell holds the same
  // image and caption everywhere.
  for (const figure of Array.from(root.querySelectorAll("figure"))) {
    figure.replaceWith(spacer(doc, "0 0 16px", ...Array.from(figure.childNodes)))
  }
  for (const list of Array.from(root.querySelectorAll("ul, ol"))) {
    style(list, `${FONT}margin:0 0 14px;padding-left:22px;font-size:15px;line-height:1.55;color:${TEXT};`)
  }
  for (const a of Array.from(root.querySelectorAll("a"))) {
    if (a.closest("table")) continue
    style(a, "color:#007bff;text-decoration:none;")
  }

  return root.innerHTML
}

// The masthead is the banner image the WordPress issues used; its alt text
// carries the name for readers whose client blocks images.
export function mastheadHtml(): string {
  return `<a href="${escapeHtml(publicSiteUrl())}" style="display:block;text-decoration:none;"><img src="${escapeHtml(emailAsset("masthead.png"))}" width="${EMAIL_WIDTH}" alt="The Triangle: ${NEWSLETTER_NAME}" style="display:block;width:100%;max-width:${EMAIL_WIDTH}px;height:auto;border:0;font-family:Helvetica,Arial,sans-serif;font-size:24px;color:${BUTTON_BLUE};"></a>`
}

export const isHttpUrl = (value: string): boolean => {
  try {
    const parsed = new URL(value)
    return parsed.protocol === "http:" || parsed.protocol === "https:"
  } catch {
    return false
  }
}

export function sponsorHtml(sponsor: NewsletterSponsor | null): string {
  if (!sponsor?.image_url) return ""
  const img = `<img src="${escapeHtml(sponsor.image_url)}" alt="${escapeHtml(sponsor.alt)}" width="${EMAIL_WIDTH}" style="display:block;width:100%;height:auto;border:0;">`
  const linked = isHttpUrl(sponsor.link_url) ? `<a href="${escapeHtml(sponsor.link_url)}">${img}</a>` : img
  return `<tr><td style="padding:0;">${linked}</td></tr>`
}

export function footerHtml(): string {
  const small = `${BUTTON_FONT}font-size:13px;color:${TEXT};text-decoration:none;`
  const link = (href: string, label: string) => `<a href="${href}" style="${small}">${label}</a>`
  const socials = SOCIAL_LINKS.map(
    (s) =>
      `<a href="${s.url}" style="text-decoration:none;"><img src="${escapeHtml(emailAsset(s.icon))}" width="32" height="32" alt="${s.label}" style="border:0;"></a>`,
  ).join(" &nbsp; ")
  return `<td align="center" style="${BUTTON_FONT}padding:24px 16px 32px;line-height:normal;color:${TEXT};"><div style="font-size:16px;">${FOOTER_TAGLINE}</div><p style="margin:16px 0;">${socials}</p><div style="${small}">${link(EMAIL_TOKENS.unsubscribe, "Unsubscribe")} &nbsp;|&nbsp; ${link(EMAIL_TOKENS.manage, "Manage your subscription")} &nbsp;|&nbsp; ${link(EMAIL_TOKENS.viewOnline, "View online")}</div></td>`
}

type EmailFields = Pick<NewsletterInput, "subject" | "preheader" | "sponsor" | "body_html">

export function renderNewsletterEmail({ subject, preheader, sponsor, body_html }: EmailFields): string {
  const pre = preheader.trim()
    ? `<div style="display:none;max-height:0;overflow:hidden;opacity:0;">${escapeHtml(preheader.trim())}</div>`
    : ""
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHtml(subject)}</title>
</head>
<body style="margin:0;padding:0;background:#ffffff;">
${pre}
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" bgcolor="#ffffff"><tr><td align="center" style="padding:0;">
<!--[if mso]><table role="presentation" width="${EMAIL_WIDTH}" align="center" cellpadding="0" cellspacing="0"><tr><td><![endif]-->
<table role="presentation" width="${EMAIL_WIDTH}" cellpadding="0" cellspacing="0" bgcolor="#ffffff" style="width:100%;max-width:${EMAIL_WIDTH}px;background:#ffffff;">
${sponsorHtml(sponsor)}
<tr><td style="padding:15px 0;">${mastheadHtml()}</td></tr>
<tr><td style="padding:20px ${SIDE_PADDING}px 0;">${bodyToEmailHtml(body_html)}</td></tr>
<tr>${footerHtml()}</tr>
</table>
<!--[if mso]></td></tr></table><![endif]-->
</td></tr></table>
</body>
</html>`
}

// For the preview pane: the tokens become harmless in-page links.
export function previewEmailHtml(email: string): string {
  const site = publicSiteUrl()
  return email
    .replaceAll(EMAIL_TOKENS.unsubscribe, `${site}/subscribe`)
    .replaceAll(EMAIL_TOKENS.manage, `${site}/subscribe`)
    .replaceAll(EMAIL_TOKENS.viewOnline, site)
}
