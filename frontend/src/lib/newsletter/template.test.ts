import { describe, expect, it } from "vitest"
import {
  articleCardHtml,
  bodyToEmailHtml,
  buildTemplateBody,
  defaultSubject,
  EMAIL_TOKENS,
  previewEmailHtml,
  RANDOM_ARTICLE_LABEL,
  READ_MORE_LABEL,
  renderNewsletterEmail,
} from "./template"

const parse = (html: string) => new DOMParser().parseFromString(html, "text/html")

describe("defaultSubject", () => {
  it("names the newsletter and the issue date", () => {
    expect(defaultSubject(new Date(2026, 9, 16, 12))).toBe("To The Point — October 16, 2026")
  })
})

describe("articleCardHtml", () => {
  it("builds headline, image, excerpt and a read-more link to the public article", () => {
    const doc = parse(
      articleCardHtml({
        title: "Discover the depths of the Drexel Archives",
        slug: "drexel-archives",
        excerpt: "Main Building once had a forge inside.",
        image_url: "https://media.example/archive.jpg",
        image_alt: "Shelves of yearbooks",
      }),
    )
    expect(doc.querySelector("h1")?.textContent).toBe("Discover the depths of the Drexel Archives")
    expect(doc.querySelector("figure img")?.getAttribute("src")).toBe("https://media.example/archive.jpg")
    expect(doc.querySelector("figure img")?.getAttribute("alt")).toBe("Shelves of yearbooks")
    const link = doc.querySelector("a")
    expect(link?.textContent).toBe(READ_MORE_LABEL)
    expect(link?.getAttribute("href")).toMatch(/\/article\/drexel-archives$/)
  })

  it("decodes WordPress entities and reduces stray markup to text", () => {
    const html = articleCardHtml({
      title: "Workers&#8217; unrest <b>live</b> &lt;img src=x onerror=alert(1)&gt;",
      slug: "x",
      excerpt: "A &amp; B",
    })
    const doc = parse(html)
    expect(doc.querySelector("h1")?.textContent).toBe("Workers’ unrest live <img src=x onerror=alert(1)>")
    expect(doc.querySelector("h1 img")).toBeNull()
    expect(doc.querySelector("h1 b")).toBeNull()
    expect(doc.querySelector("p")?.textContent).toBe("A & B")
  })

  it("leaves out the image when the article has none", () => {
    expect(articleCardHtml({ title: "T", slug: "t", excerpt: "" })).not.toContain("<figure")
  })
})

describe("buildTemplateBody", () => {
  it("lays out intro, label, cards, question, random-article button and comic in template order", () => {
    const body = buildTemplateBody({
      articles: [
        { title: "One", slug: "one", excerpt: "" },
        { title: "Two", slug: "two", excerpt: "" },
      ],
      comic: { title: "Abstraction juice", slug: "comic", image_url: "https://media.example/comic.png" },
      question: "What is your favorite form of egg to eat?",
    })
    const doc = parse(body)
    expect(Array.from(doc.querySelectorAll("h1")).map((h) => h.textContent)).toEqual(["One", "Two"])
    expect(Array.from(doc.querySelectorAll("blockquote")).map((q) => q.textContent)).toEqual([
      "Read the latest from The Triangle",
    ])
    expect(doc.body.textContent).toContain("Question of the day: What is your favorite form of egg to eat?")
    const random = Array.from(doc.querySelectorAll("a")).find((a) => a.textContent === RANDOM_ARTICLE_LABEL)
    expect(random?.getAttribute("href")).toMatch(/\/random$/)
    // The button is a line of its own, so the email renders it as a button.
    expect(random?.parentElement?.childNodes).toHaveLength(1)
    // The comic follows the button.
    const last = doc.body.lastElementChild
    expect(last?.querySelector("img")?.getAttribute("src")).toBe("https://media.example/comic.png")
  })

  it("keeps the random-article button and leaves a placeholder question when nothing else is given", () => {
    const body = buildTemplateBody({ articles: [] })
    expect(body).toContain("[Your question here]")
    expect(body).toContain(RANDOM_ARTICLE_LABEL)
    expect(body).not.toContain("<figure")
  })
})

describe("bodyToEmailHtml", () => {
  it("turns a line that is only a link into a button table", () => {
    const doc = parse(bodyToEmailHtml('<p><a href="https://x.test/a">Read</a></p><p>Some <a href="https://x.test/b">inline</a> link</p>'))
    const buttons = doc.querySelectorAll("table a")
    expect(buttons).toHaveLength(1)
    expect(buttons[0].getAttribute("href")).toBe("https://x.test/a")
    // The inline link stays inline.
    expect(doc.querySelector("p a")?.getAttribute("href")).toBe("https://x.test/b")
  })

  it("puts a divider above every headline but the first", () => {
    const doc = parse(bodyToEmailHtml("<h1>One</h1><p>a</p><h1>Two</h1><p>b</p><h1>Three</h1>"))
    const dividers = Array.from(doc.body.querySelectorAll("div")).filter((d) =>
      d.getAttribute("style")?.includes("border-top"),
    )
    expect(dividers).toHaveLength(2)
    expect(doc.body.firstElementChild?.tagName).toBe("H1")
  })

  it("leaves nothing Outlook's Word engine can't lay out", () => {
    const doc = parse(
      bodyToEmailHtml(
        '<div>Plain text</div><div><a href="https://x.test/a">Read</a></div>' +
          '<figure class="wp-caption"><img src="https://m.test/a.jpg" alt=""><figcaption>Credit</figcaption></figure>',
      ),
    )
    expect(doc.querySelector("figure, figcaption")).toBeNull()
    expect(doc.querySelector("p")?.textContent).toBe("Plain text")
    // Spacing is cell padding, never margin on a table.
    for (const table of Array.from(doc.querySelectorAll("table"))) {
      expect(table.getAttribute("style") ?? "").not.toContain("margin")
    }
    const button = doc.querySelector("td[bgcolor]")
    expect(button?.getAttribute("style")).toContain("padding:10px 18px")
    expect(button?.querySelector("a")?.getAttribute("style")).not.toContain("padding")
    expect(doc.querySelector("img")?.closest("td")?.textContent).toBe("Credit")
  })

  it("sizes images for the email column and drops lazy loading", () => {
    const doc = parse(bodyToEmailHtml('<figure class="wp-caption"><img src="https://m.test/a.jpg" alt="" loading="lazy"></figure>'))
    const img = doc.querySelector("img")
    expect(img?.hasAttribute("loading")).toBe(false)
    expect(img?.getAttribute("width")).toBe("552")
  })
})

describe("renderNewsletterEmail", () => {
  const fields = {
    subject: "To The Point — October 16, 2026",
    preheader: "Archives, renaming and live music",
    sponsor: { image_url: "https://m.test/ad.png", link_url: "javascript:alert(1)", alt: "Ad" },
    body_html: "<p>Hello</p>",
  }

  it("keeps the per-recipient tokens for the sender to fill", () => {
    const html = renderNewsletterEmail(fields)
    expect(html).toContain(EMAIL_TOKENS.unsubscribe)
    expect(html).toContain(EMAIL_TOKENS.manage)
    expect(html).toContain(EMAIL_TOKENS.viewOnline)
    expect(previewEmailHtml(html)).not.toContain("{{")
  })

  it("renders the sponsor image but refuses a non-http link", () => {
    const doc = parse(renderNewsletterEmail(fields))
    expect(doc.querySelector('img[src="https://m.test/ad.png"]')).not.toBeNull()
    expect(doc.querySelector('a[href^="javascript"]')).toBeNull()
  })

  it("pins the card to 600px for Outlook, which ignores max-width", () => {
    expect(renderNewsletterEmail(fields)).toMatch(/<!--\[if mso\]><table role="presentation" width="600"/)
  })

  it("includes the masthead, hidden preheader and subject title", () => {
    const doc = parse(renderNewsletterEmail(fields))
    expect(doc.title).toBe(fields.subject)
    expect(doc.body.textContent).toContain("To The Point")
    expect(doc.body.textContent).toContain(fields.preheader)
  })
})
