import { describe, expect, it } from "vitest"

import { shortcodeToEmbed } from "../lib/embedUrl"
import legacy from "./__fixtures__/legacy-shortcodes.json"
import {
  EMBED_CONTENT_TYPE,
  articleHtmlToTrix,
  buildEmbedCardHtml,
  findInvalidEmbeds,
  isAcceptableAudioUrl,
  trixHtmlToArticle,
} from "./trixArticleHtml"

const attachment = (data: Record<string, unknown>, attributes: Record<string, unknown> = {}) =>
  `<figure data-trix-attachment='${JSON.stringify(data)}' data-trix-attributes='${JSON.stringify(attributes)}'><img src="${String(data.url)}"></figure>`

describe("trixHtmlToArticle today", () => {
  it("reduces an image attachment to a wp-caption figure", () => {
    const out = trixHtmlToArticle(
      `<div>${attachment({ contentType: "image/jpeg", url: "https://m.test/a.jpg", alt: "A" }, { caption: "Cap" })}</div>`,
    )
    expect(out).toBe(
      `<div><figure class="wp-caption"><img src="https://m.test/a.jpg" alt="A" loading="lazy"><figcaption class="wp-caption-text">Cap</figcaption></figure></div>`,
    )
  })

  it("leaves a non-image attachment exactly as Trix wrote it", () => {
    const html = `<div>${attachment({ contentType: "application/pdf", url: "https://m.test/a.pdf", filename: "a.pdf" })}</div>`
    expect(trixHtmlToArticle(html)).toBe(html)
  })

  it("leaves a still-uploading image alone", () => {
    const html = `<div>${attachment({ contentType: "image/png", url: "blob:https://cms/abc" })}</div>`
    expect(trixHtmlToArticle(html)).toBe(html)
  })
})

describe("articleHtmlToTrix today", () => {
  it("rebuilds a wp-caption figure as a Trix attachment in its own block", () => {
    const out = articleHtmlToTrix(
      `<figure class="wp-caption aligncenter"><img src="https://m.test/a.jpg" alt="A"><figcaption class="wp-caption-text">Cap</figcaption></figure>`,
    )
    expect(out).toContain("data-trix-attachment=")
    expect(out).toContain("&quot;caption&quot;:&quot;Cap&quot;")
    expect(out.startsWith("<div><figure")).toBe(true)
  })

  it("returns plain prose untouched", () => {
    expect(articleHtmlToTrix("<p>Hello</p>")).toBe("<p>Hello</p>")
  })
})

const SPOTIFY = "https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5"
const SPOTIFY_SC = `[spotify url="${SPOTIFY}" /]`
const AUDIO = "https://media.test/wp-content/uploads/2026/10/ep1.mp3"

const YOUTUBE = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
const YOUTUBE_CANONICAL = `<div>[youtube ${YOUTUBE}]</div>`

const embedFigure = (data: Record<string, unknown>) =>
  `<div><figure data-trix-attachment='${JSON.stringify(data)}' data-trix-attributes='{}'><div class="embed-card">stored</div></figure></div>`

const youtubeWithOriginal = (embedOriginal: string) =>
  embedFigure({ contentType: EMBED_CONTENT_TYPE, embedProvider: "youtube", embedUrl: YOUTUBE, embedOriginal })

// jsdom serializes attribute values with " as &quot; and & as &amp;. The &amp;
// replace runs last so a literal "&amp;quot;" in the JSON is not double-decoded.
const readAttachment = (html: string) => {
  const m = /data-trix-attachment=(?:'([^']*)'|"([^"]*)")/.exec(html)
  const raw = m?.[1] ?? m?.[2]
  return raw === undefined
    ? null
    : (JSON.parse(raw.replace(/&quot;/g, '"').replace(/&amp;/g, "&")) as Record<string, unknown>)
}

describe("load: shortcode blocks become embed cards", () => {
  it("turns a <div> holding one new shortcode into a content attachment", () => {
    const out = articleHtmlToTrix(`<div>${SPOTIFY_SC}</div>`)
    const data = readAttachment(out)
    expect(data).toMatchObject({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: SPOTIFY })
    expect(String(data?.content)).toContain("embed-card")
    expect(String(data?.content)).toContain("Spotify")
    expect(out).not.toContain("[spotify")
  })

  it("turns a legacy <p>[youtube ...]</p> into a card that remembers its original block", () => {
    const block = `<p>[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;w=560&amp;h=315]</p>`
    const data = readAttachment(articleHtmlToTrix(block))
    expect(data).toMatchObject({ embedProvider: "youtube", embedUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ" })
    expect(data?.embedOriginal).toBe(block)
  })

  it("turns an [audio src] block into an audio attachment", () => {
    const data = readAttachment(articleHtmlToTrix(`<div>[audio src="${AUDIO}" /]</div>`))
    expect(data).toMatchObject({ contentType: "audio/mpeg", url: AUDIO, href: AUDIO })
  })

  it("gives a loaded audio attachment an editor card with the filename", () => {
    const data = readAttachment(articleHtmlToTrix(`<div>[audio src="${AUDIO}" /]</div>`))
    expect(data?.content).toMatch(/embed-card[\s\S]*ep1\.mp3/)
  })

  it("renders the audio card inside the loaded figure", () => {
    expect(articleHtmlToTrix(`<div>[audio src="${AUDIO}" /]</div>`)).toContain('<div class="embed-card">')
  })

  it("leaves a shortcode inside prose as text", () => {
    const html = `<p>Listen: ${SPOTIFY_SC}</p>`
    expect(articleHtmlToTrix(html)).toBe(html)
  })

  it("leaves an invalid shortcode as text", () => {
    const html = `<div>[spotify url="https://spotify.link/x" /]</div>`
    expect(articleHtmlToTrix(html)).toBe(html)
  })

  it("rebuilds the card of a stored embed figure and discards stored content", () => {
    const out = articleHtmlToTrix(
      embedFigure({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: SPOTIFY, content: '<img src=x onerror="alert(1)">' }),
    )
    expect(out).not.toContain("onerror")
    expect(String(readAttachment(out)?.content)).toContain("embed-card")
  })

  it("turns a stored embed figure with invalid attributes into an inert error card", () => {
    const out = articleHtmlToTrix(embedFigure({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: "javascript:alert(1)", content: "x" }))
    const data = readAttachment(out)
    expect(data?.embedUrl).toBeUndefined()
    expect(String(data?.content)).toContain("embed-card--invalid")
  })

  it("rebuilds a stored embed attachment that is not a <figure>", () => {
    const out = articleHtmlToTrix(
      `<div><div data-trix-attachment='${JSON.stringify({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: SPOTIFY, content: '<img src=x onerror="alert(1)">' })}'></div></div>`,
    )
    expect(out).not.toContain("onerror")
  })

  it("leaves a bare top-level legacy shortcode as text", () => {
    const bare = `[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;w=560&amp;h=315]`
    expect(articleHtmlToTrix(bare)).toBe(bare)
  })

  it("turns a stored audio figure with a foreign URL into an inert error card", () => {
    const out = articleHtmlToTrix(embedFigure({ contentType: "audio/mpeg", url: "https://media.test/evil.wav" }))
    expect(String(readAttachment(out)?.content)).toContain("embed-card--invalid")
  })
})

describe("save: embed cards become shortcodes", () => {
  it("emits the canonical shortcode in its own div", () => {
    const html = articleHtmlToTrix(`<div>${SPOTIFY_SC}</div>`)
    expect(trixHtmlToArticle(html)).toBe(`<div>${SPOTIFY_SC}</div>`)
  })

  it("emits a legacy block byte-for-byte when unedited", () => {
    const block = `<p class="media-box">[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;w=560&amp;h=315]</p>`
    expect(trixHtmlToArticle(articleHtmlToTrix(block))).toBe(block)
  })

  it("does not emit a crafted embedOriginal that resolves elsewhere", () => {
    const out = trixHtmlToArticle(
      embedFigure({
        contentType: EMBED_CONTENT_TYPE,
        embedProvider: "youtube",
        embedUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
        embedOriginal: `<p>[youtube https://www.youtube.com/watch?v=AAAAAAAAAAA]</p>`,
      }),
    )
    expect(out).toBe(`<div>[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ]</div>`)
  })

  it("does not emit an embedOriginal that is not one shortcode in one wrapper", () => {
    const out = trixHtmlToArticle(
      embedFigure({
        contentType: EMBED_CONTENT_TYPE,
        embedProvider: "youtube",
        embedUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
        embedOriginal: `<p>[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ]</p><script>1</script>`,
      }),
    )
    expect(out).toBe(`<div>[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ]</div>`)
  })

  it("does not emit an embedOriginal whose wrapper carries an event handler", () => {
    const out = trixHtmlToArticle(
      embedFigure({
        contentType: EMBED_CONTENT_TYPE,
        embedProvider: "youtube",
        embedUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
        embedOriginal: `<p onclick="alert(1)">[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ]</p>`,
      }),
    )
    expect(out).toBe(`<div>[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ]</div>`)
  })

  it("does not emit an embedOriginal whose wrapper carries site classes", () => {
    const out = trixHtmlToArticle(youtubeWithOriginal(`<p class="comments-open paywall">[youtube ${YOUTUBE}]</p>`))
    expect(out).toBe(YOUTUBE_CANONICAL)
  })

  it("emits an embedOriginal whose wrapper class is media-box", () => {
    const block = `<p class="media-box">[youtube ${YOUTUBE}]</p>`
    expect(trixHtmlToArticle(youtubeWithOriginal(block))).toBe(block)
  })

  it("keeps two embeds in one block in their original order", () => {
    const fig = (embedUrl: string) =>
      `<figure data-trix-attachment='${JSON.stringify({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl })}' data-trix-attributes='{}'></figure>`
    const other = "https://open.spotify.com/episode/1111111111111111111111"
    const out = trixHtmlToArticle(`<div>${fig(SPOTIFY)}${fig(other)}</div>`)
    expect(out).toBe(`<div>${SPOTIFY_SC}</div><div>[spotify url="${other}" /]</div>`)
  })

  it("removes the quote that is left empty when its embed moves out", () => {
    const out = trixHtmlToArticle(
      `<blockquote>${embedFigure({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: SPOTIFY }).slice(5, -6)}</blockquote>`,
    )
    expect(out).toBe(`<div>${SPOTIFY_SC}</div>`)
  })

  it("moves an embed that shares its block with text out to a top-level block", () => {
    const html = `<div>Intro${embedFigure({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: SPOTIFY }).slice(5)}`
    expect(trixHtmlToArticle(html)).toBe(`<div>Intro</div><div>${SPOTIFY_SC}</div>`)
  })

  it("does not emit an embedOriginal with an img inside the brackets", () => {
    const out = trixHtmlToArticle(youtubeWithOriginal(`<p>[youtube ${YOUTUBE}<img src="x" onerror="alert(1)">]</p>`))
    expect(out).toBe(YOUTUBE_CANONICAL)
  })

  it("does not emit an embedOriginal with a javascript: link inside the brackets", () => {
    const out = trixHtmlToArticle(youtubeWithOriginal(`<p>[youtube <a href="javascript:alert(1)">${YOUTUBE}</a>]</p>`))
    expect(out).toBe(YOUTUBE_CANONICAL)
  })

  it("does not emit an embedOriginal with an svg inside the brackets", () => {
    const out = trixHtmlToArticle(youtubeWithOriginal(`<p>[youtube ${YOUTUBE}<svg onload="alert(1)"></svg>]</p>`))
    expect(out).toBe(YOUTUBE_CANONICAL)
  })

  it("does not emit an embedOriginal whose wrapper carries a site data hook", () => {
    const out = trixHtmlToArticle(youtubeWithOriginal(`<p data-comment-form="">[youtube ${YOUTUBE}]</p>`))
    expect(out).toBe(YOUTUBE_CANONICAL)
  })

  it("does not emit an embedOriginal whose wrapper style is not a text alignment", () => {
    const out = trixHtmlToArticle(youtubeWithOriginal(`<p style="position:fixed">[youtube ${YOUTUBE}]</p>`))
    expect(out).toBe(YOUTUBE_CANONICAL)
  })

  it("emits a bare-text embedOriginal verbatim", () => {
    const bare = `[youtube ${YOUTUBE}&amp;w=560&amp;h=315]`
    // Escape & for the attribute so the stored JSON value holds the literal &amp;.
    const html = youtubeWithOriginal(bare).replace(/&/g, "&amp;")
    expect(trixHtmlToArticle(html)).toBe(bare)
  })

  it("saves a loaded audio block as the same shortcode", () => {
    const html = `<div>[audio src="${AUDIO}" /]</div>`
    expect(trixHtmlToArticle(articleHtmlToTrix(html))).toBe(html)
  })

  it("emits [audio src] for a server-hosted MP3", () => {
    const out = trixHtmlToArticle(embedFigure({ contentType: "audio/mpeg", url: AUDIO, href: AUDIO, filename: "ep1.mp3" }))
    expect(out).toBe(`<div>[audio src="${AUDIO}" /]</div>`)
  })

  it("round-trips an uppercase .MP3", () => {
    const upper = "https://media.test/wp-content/uploads/2026/10/EP1.MP3"
    expect(trixHtmlToArticle(articleHtmlToTrix(`<div>[audio src="${upper}" /]</div>`))).toBe(`<div>[audio src="${upper}" /]</div>`)
  })

  it("passes an invalid embed figure through so findInvalidEmbeds can see it", () => {
    const html = embedFigure({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: "nope" })
    const out = trixHtmlToArticle(html)
    expect(out).toContain("data-trix-attachment")
    expect(findInvalidEmbeds(out)).toEqual(["Embed 1 has an invalid URL. Remove the block and insert it again."])
  })

  it("passes a still-uploading audio attachment through and reports it", () => {
    const html = embedFigure({ contentType: "audio/mpeg", url: "blob:https://cms/abc" })
    const out = trixHtmlToArticle(html)
    expect(out).toBe(html)
    expect(findInvalidEmbeds(out)).toEqual(["Audio 1 has not finished uploading."])
  })

  it("reports an uploaded audio attachment with an unacceptable URL", () => {
    const out = trixHtmlToArticle(embedFigure({ contentType: "audio/mpeg", url: "https://media.test/a.wav" }))
    expect(findInvalidEmbeds(out)).toEqual(["Audio 1 has an invalid URL. Remove the block and upload it again."])
  })

  it("numbers an invalid embed by its position among all embeds", () => {
    const bad = embedFigure({ contentType: EMBED_CONTENT_TYPE, embedProvider: "spotify", embedUrl: "nope" })
    const out = `<div>${SPOTIFY_SC}</div><div>${SPOTIFY_SC}</div>${bad}`
    expect(findInvalidEmbeds(out)).toEqual(["Embed 3 has an invalid URL. Remove the block and insert it again."])
  })

  it("reports nothing for a clean article", () => {
    expect(findInvalidEmbeds(`<div>${SPOTIFY_SC}</div><p>x</p>`)).toEqual([])
  })
})

describe("legacy corpus round trip", () => {
  const withEmbed = legacy.distinct.filter((entry) => shortcodeToEmbed(/\[[^\]]*\]/.exec(entry.block)?.[0] ?? ""))
  const wrapped = withEmbed.filter((entry) => entry.wrapper !== "bare")
  const bare = withEmbed.filter((entry) => entry.wrapper === "bare")

  it.each(wrapped.map((entry) => entry.block))("keeps %s unchanged", (block) => {
    expect(trixHtmlToArticle(articleHtmlToTrix(block))).toBe(block)
  })

  it.each(bare.map((entry) => entry.block))("leaves bare %s as text", (block) => {
    expect(trixHtmlToArticle(articleHtmlToTrix(block))).toBe(block)
  })
})

describe("isAcceptableAudioUrl", () => {
  it("accepts https under uploads with .mp3", () => {
    expect(isAcceptableAudioUrl(AUDIO + "?v=1")).toBe(AUDIO)
  })
  it("rejects a blob URL", () => {
    expect(isAcceptableAudioUrl("blob:https://cms/abc")).toBeNull()
  })
  it("rejects http", () => {
    expect(isAcceptableAudioUrl("http://media.test/wp-content/uploads/a.mp3")).toBeNull()
  })
  it("rejects a non-mp3 path", () => {
    expect(isAcceptableAudioUrl("https://media.test/wp-content/uploads/a.wav")).toBeNull()
  })
})

describe("buildEmbedCardHtml", () => {
  it("links the URL so the author can open it from the editor", () => {
    const url = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
    const card = new DOMParser().parseFromString(buildEmbedCardHtml("youtube", url), "text/html")
    const link = card.querySelector("a")
    expect(link?.getAttribute("href")).toBe(url)
    expect(link?.textContent).toBe(url)
  })

  it("escapes the URL as text", () => {
    const html = buildEmbedCardHtml("youtube", `https://www.youtube.com/watch?v=<b>`)
    const card = new DOMParser().parseFromString(html, "text/html")
    expect(card.querySelector("b")).toBeNull()
    expect(card.querySelector("a")?.textContent).toBe("https://www.youtube.com/watch?v=<b>")
  })
})
