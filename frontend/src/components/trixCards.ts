// Article cards in an editor body, found in Trix's serialized blocks (one
// top-level element per block). A card is a heading block through the next
// block that holds nothing but a link labelled `endLinkText`, which is the
// shape the newsletter's articleCardHtml inserts: headline, optional image,
// optional excerpt, "Click Here to Read More". A heading with no such link
// before the next heading is not a card.
export type CardSpan = {
  // Indexes of the heading block and the link block, inclusive.
  start: number
  end: number
  // The link block's href: how a card is recognised again after the document
  // has changed under an open picker.
  href: string
}

const soleLinkHref = (block: Element): string | null => {
  const meaningful = Array.from(block.childNodes).filter(
    (node) =>
      !(node.nodeType === Node.TEXT_NODE && !node.textContent?.trim()) &&
      !(node.nodeType === Node.ELEMENT_NODE && (node as Element).tagName === "BR") &&
      node.nodeType !== Node.COMMENT_NODE,
  )
  if (meaningful.length !== 1 || meaningful[0].nodeType !== Node.ELEMENT_NODE) return null
  const anchor = meaningful[0] as Element
  return anchor.tagName === "A" ? anchor.getAttribute("href") : null
}

export const findCards = (blocks: Element[], endLinkText: string): CardSpan[] => {
  const cards: CardSpan[] = []
  for (let start = 0; start < blocks.length; start++) {
    if (blocks[start].tagName !== "H1") continue
    for (let end = start + 1; end < blocks.length; end++) {
      if (blocks[end].tagName === "H1") break
      const href = soleLinkHref(blocks[end])
      if (href && blocks[end].textContent?.trim() === endLinkText) {
        cards.push({ start, end, href })
        start = end
        break
      }
    }
  }
  return cards
}
