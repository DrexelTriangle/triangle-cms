import { articleUrl, publicSiteUrl } from "../../auth/urls"
import type { NewsletterInput, NewsletterSponsor } from "./types"

// "To The Point", The Triangle's emailed digest. The fixed parts (sponsor
// banner, masthead, footer) live here; everything between the masthead and the
// footer is one document edited in the same Trix editor as articles, seeded by
// buildTemplateBody and turned into email HTML by renderNewsletterEmail.
//
// The editor has no email-specific blocks, so a few ordinary Trix formats carry
// the template's structure. The editor's formatting guide lists them:
//   Heading            -> article headline, with a divider above all but the first
//   Quote              -> centred section label ("Read the latest from The Triangle")
//   A line that is only a link -> a button

export const NEWSLETTER_NAME = "To The Point"
export const MASTHEAD_BLUE = "#055b9d"
const DIVIDER_BLUE = "#2f8fd3"
const TEXT = "#1f2328"
const MUTED = "#57606a"
const EMAIL_WIDTH = 600

export const READ_MORE_LABEL = "Click Here to Read More"
export const RANDOM_ARTICLE_LABEL = "Check out a Random Article"

export const FOOTER_TAGLINE = "The Independent Student Newspaper at Drexel University"
export const SOCIAL_LINKS = [
  { label: "X", url: "https://x.com/DrexelTriangle" },
  { label: "Instagram", url: "https://www.instagram.com/drexeltriangle/" },
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

const imageHtml = (url: string | undefined, alt: string | undefined) =>
  url ? `<figure><img src="${escapeHtml(url)}" alt="${text(alt ?? "")}"></figure>` : ""

// One story: headline, lead image, excerpt and the read-more button. Plain
// article HTML, so every part stays editable once it is in the editor.
export function articleCardHtml(article: TemplateArticle): string {
  const excerpt = text(article.excerpt)
  return [
    `<h1>${text(article.title)}</h1>`,
    imageHtml(article.image_url, article.image_alt || decodeEntities(article.title)),
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
  const parts = [`<p>${escapeHtml(INTRO_PLACEHOLDER)}</p>`, "<blockquote>Read the latest from The Triangle</blockquote>"]
  parts.push(...articles.map(articleCardHtml))
  parts.push(
    `<p><strong>Question of the day:</strong> ${
      question?.trim() ? text(question) : "[Your question here]"
    } (Reply to this email with your response!)</p>`,
  )
  // The site's /random page, the same as the Random Article link in its header.
  parts.push(`<p><a href="${escapeHtml(`${publicSiteUrl()}/random`)}">${RANDOM_ARTICLE_LABEL}</a></p>`)
  if (comic) {
    parts.push(imageHtml(comic.image_url, comic.image_alt || decodeEntities(comic.title)))
  }
  return parts.join("")
}

// ── Email rendering ──────────────────────────────────────────────────────────

const style = (el: Element, css: string) => el.setAttribute("style", css)

const FONT = "font-family:Helvetica,Arial,sans-serif;"

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

// A table, not a styled <a>: Outlook ignores padding on inline elements, so
// the padding is on the cell.
const buttonFor = (doc: Document, link: HTMLAnchorElement): HTMLTableElement => {
  const button = doc.createElement("table")
  button.setAttribute("role", "presentation")
  button.setAttribute("cellpadding", "0")
  button.setAttribute("cellspacing", "0")
  const cell = button.insertRow().insertCell()
  cell.setAttribute("bgcolor", MASTHEAD_BLUE)
  style(cell, "border-radius:4px;padding:10px 18px;")
  const a = doc.createElement("a")
  a.setAttribute("href", link.getAttribute("href") ?? "#")
  a.textContent = link.textContent?.trim() || READ_MORE_LABEL
  style(a, `${FONT}font-size:14px;font-weight:bold;color:#ffffff;text-decoration:none;`)
  cell.appendChild(a)
  return spacer(doc, "8px 0 24px", button)
}

const divider = (doc: Document) => {
  const rule = doc.createElement("div")
  style(rule, `border-top:2px solid ${DIVIDER_BLUE};font-size:0;line-height:0;`)
  rule.innerHTML = "&nbsp;"
  return spacer(doc, "32px 0 24px", rule)
}

const BLOCKS = "div, p, h1, h2, h3, h4, ul, ol, blockquote, table, figure"

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

  for (const el of Array.from(root.querySelectorAll("p, div"))) {
    if (el.closest("figure")) continue
    const link = soleLink(el)
    if (link) {
      el.replaceWith(buttonFor(doc, link))
      continue
    }
    if (el.querySelector("figure, img")) continue
    // Only a leaf block can become <p>; a <p> can't hold other blocks.
    const text = el.tagName === "DIV" && !el.querySelector(BLOCKS) ? renamed(el, "p") : el
    style(text, `${FONT}margin:0 0 14px;font-size:15px;line-height:1.55;color:${TEXT};`)
  }

  let headlines = 0
  for (const h of Array.from(root.querySelectorAll("h1"))) {
    if (headlines > 0) h.before(divider(doc))
    headlines += 1
    style(h, `${FONT}margin:0 0 14px;font-size:24px;line-height:1.3;font-weight:normal;color:${TEXT};`)
  }

  for (const quote of Array.from(root.querySelectorAll("blockquote"))) {
    style(quote, `${FONT}margin:24px 0 20px;padding:0;text-align:center;font-size:14px;color:${TEXT};`)
  }

  for (const img of Array.from(root.querySelectorAll("img"))) {
    img.removeAttribute("loading")
    img.setAttribute("width", String(EMAIL_WIDTH - 48))
    style(img, "display:block;width:100%;max-width:100%;height:auto;border:0;")
  }
  for (const caption of Array.from(root.querySelectorAll("figcaption"))) {
    style(renamed(caption, "div"), `${FONT}padding:6px 0 0;font-size:12px;line-height:1.4;color:${MUTED};`)
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
    style(a, `color:${MASTHEAD_BLUE};text-decoration:underline;`)
  }

  return root.innerHTML
}

export function mastheadHtml(): string {
  return `<table role="presentation" width="100%" cellpadding="0" cellspacing="0" bgcolor="${MASTHEAD_BLUE}"><tr><td align="center" style="padding:22px 16px;background:${MASTHEAD_BLUE};color:#ffffff;font-family:Georgia,'Times New Roman',serif;"><div style="font-size:15px;letter-spacing:2px;line-height:1.2;">THE TRIANGLE</div><div style="font-size:30px;line-height:1.2;">${NEWSLETTER_NAME}</div></td></tr></table>`
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
  return `<tr><td style="padding:0 0 8px;">${linked}</td></tr>`
}

export function footerHtml(): string {
  const link = (href: string, label: string) =>
    `<a href="${href}" style="color:${MUTED};text-decoration:none;">${label}</a>`
  const socials = SOCIAL_LINKS.map((s) => link(s.url, s.label)).join(" &nbsp;·&nbsp; ")
  return `<td align="center" style="${FONT}padding:28px 24px 32px;font-size:12px;line-height:1.6;color:${MUTED};"><div style="font-size:13px;color:${TEXT};">${FOOTER_TAGLINE}</div><p style="margin:8px 0;">${socials}</p><div>${link(EMAIL_TOKENS.unsubscribe, "Unsubscribe")} &nbsp;|&nbsp; ${link(EMAIL_TOKENS.manage, "Manage your subscription")} &nbsp;|&nbsp; ${link(EMAIL_TOKENS.viewOnline, "View online")}</div></td>`
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
<body style="margin:0;padding:0;background:#f3f4f6;">
${pre}
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" bgcolor="#f3f4f6"><tr><td align="center" style="padding:16px 8px;">
<!--[if mso]><table role="presentation" width="${EMAIL_WIDTH}" align="center" cellpadding="0" cellspacing="0"><tr><td><![endif]-->
<table role="presentation" width="${EMAIL_WIDTH}" cellpadding="0" cellspacing="0" bgcolor="#ffffff" style="width:100%;max-width:${EMAIL_WIDTH}px;background:#ffffff;">
${sponsorHtml(sponsor)}
<tr><td>${mastheadHtml()}</td></tr>
<tr><td style="padding:24px 24px 8px;">${bodyToEmailHtml(body_html)}</td></tr>
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
