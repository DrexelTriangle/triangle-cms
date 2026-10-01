// Resized WebP renditions the API attaches to library images (`variants` on
// media items, `featured_image_variants` on articles), narrowest first. They
// are made in the background, so any image may not have them yet; the
// original URL always stays the src.
export type ImageVariant = {
  url: string
  width: number
  height: number
}

// srcSetFor turns renditions into a srcset, or undefined when there are none
// so React omits the attribute and the browser just loads the src.
//
// This matters most in the editor's grids: the library is mostly camera
// originals, and a page of 48 tiles was 48 full-size files for 150px squares.
export function srcSetFor(variants?: ImageVariant[] | null): string | undefined {
  if (!variants?.length) return undefined
  const entries = variants
    .filter((variant) => variant.url && variant.width > 0)
    .map((variant) => `${variant.url} ${variant.width}w`)
  return entries.length ? entries.join(", ") : undefined
}
