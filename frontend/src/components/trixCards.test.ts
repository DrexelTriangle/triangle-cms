import { describe, expect, it } from "vitest"

import { findCards } from "./trixCards"

const READ_MORE = "Click Here to Read More"
const blocks = (html: string) => Array.from(new DOMParser().parseFromString(html, "text/html").body.children)

describe("findCards", () => {
  it("spans each headline through its read-more link, with or without an image and excerpt", () => {
    const html = [
      "<div>Intro</div>",
      '<div><a href="https://www.thetriangle.org">Read the latest</a></div>',
      "<h1>A</h1>",
      '<div><figure data-trix-attachment="{}"><img src="a.jpg"></figure></div>',
      "<div>Excerpt A</div>",
      `<div><a href="https://www.thetriangle.org/article/a">${READ_MORE}</a></div>`,
      "<h1>B</h1>",
      `<div><a href="https://www.thetriangle.org/article/b">${READ_MORE}</a></div>`,
      "<div><strong>Question of the day:</strong> x</div>",
    ].join("")
    expect(findCards(blocks(html), READ_MORE)).toEqual([
      { start: 2, end: 5, href: "https://www.thetriangle.org/article/a" },
      { start: 6, end: 7, href: "https://www.thetriangle.org/article/b" },
    ])
  })

  it("does not treat a headline as a card when the next headline comes before any read-more link", () => {
    const html = [
      "<h1>Just a heading</h1>",
      "<div>Text</div>",
      "<h1>B</h1>",
      `<div><a href="/b">${READ_MORE}</a></div>`,
    ].join("")
    expect(findCards(blocks(html), READ_MORE)).toEqual([{ start: 2, end: 3, href: "/b" }])
  })

  it("needs the link alone in its block, with the exact label", () => {
    const html = [
      "<h1>A</h1>",
      `<div>See <a href="/a">${READ_MORE}</a></div>`,
      '<div><a href="/a">Read more</a></div>',
    ].join("")
    expect(findCards(blocks(html), READ_MORE)).toEqual([])
  })
})
