"""Instagram image sidecar for the CMS.

Scalene shows Instagram posts in its own holder, which needs the post's
images on our media origin. Instagram hands a plain HTTP fetch an empty
shell, and its oEmbed thumbnail needs a Meta app token, so this service loads
the post's embed page in a real browser and reads the image URLs out of it.

Stateless, like the imaging sidecar. The CMS sends a post URL and gets a list
of image URLs back; the CMS copies the bytes into the media library and owns
everything after that. The image URLs are signed and expire within days, so
they are only ever used right away. If this service is missing, the editor
says so and the writer can still link the post.
"""

from __future__ import annotations

import asyncio
import html
import logging
import os
import re

from camoufox.async_api import AsyncCamoufox
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

# Only a post or a reel. The CMS already checks this; the sidecar checks again
# so it can never be pointed at an arbitrary page.
POST_URL = re.compile(r"^https://www\.instagram\.com/(p|reel)/[A-Za-z0-9_-]+/$")

TIMEOUT_MS = int(os.getenv("SCRAPE_TIMEOUT_MS", "20000"))
# The CMS keeps at most ten; sending more is wasted bytes.
MAX_IMAGES = 10

# The embed page lists the post's own images and, under them, a row of "more
# posts" thumbnails and the author's profile picture. The post images come from
# the t51.* media paths at full size; profile pictures live under t51.2885-19
# and the thumbnails carry a small-size hint (s150x150, p240x240).
COLLECT_IMAGES = """
() => {
  const seen = new Set();
  const out = [];
  const keep = (img) => {
    const src = img.currentSrc || img.src || "";
    if (!/cdninstagram\\.com\\//.test(src)) return;
    if (/\\/t51\\.2885-19\\//.test(src)) return;
    if (/[sp]\\d{2,3}x\\d{2,3}/.test(src)) return;
    const key = src.split("?")[0].split("/").pop();
    if (!key || seen.has(key)) return;
    seen.add(key);
    out.push(src);
  };
  document.querySelectorAll("img.EmbeddedMediaImage").forEach(keep);
  document.querySelectorAll("img").forEach(keep);
  return out;
}
"""

log = logging.getLogger("scraper")
logging.basicConfig(level=logging.INFO)

app = FastAPI(title="triangle-cms-scraper")

# One browser at a time: each request launches its own Firefox, and two in
# flight would double the memory for no gain on a one-writer workflow.
_lock = asyncio.Lock()


class InstagramRequest(BaseModel):
    url: str


class InstagramResponse(BaseModel):
    images: list[str]


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/instagram", response_model=InstagramResponse)
async def instagram(body: InstagramRequest) -> InstagramResponse:
    url = body.url.strip()
    if not POST_URL.match(url):
        raise HTTPException(status_code=400, detail="url must be an Instagram post or reel link")
    log.info("instagram: loading %s", url)
    async with _lock:
        try:
            images = await asyncio.wait_for(_collect(url + "embed/"), timeout=TIMEOUT_MS / 1000 + 5)
        except asyncio.TimeoutError as exc:
            log.warning("instagram: timed out on %s", url)
            raise HTTPException(status_code=504, detail="the post took too long to load") from exc
        except Exception as exc:  # noqa: BLE001 - any browser failure is a 502 to the CMS
            log.warning("instagram: failed on %s: %s", url, exc)
            raise HTTPException(status_code=502, detail="could not load the post") from exc
    log.info("instagram: %d image(s) on %s", len(images), url)
    return InstagramResponse(images=images[:MAX_IMAGES])


# Post images in the page markup, for carousel frames the page has not drawn
# yet (they sit in the HTML with their srcset before the slider shows them).
MARKUP_IMAGE = re.compile(r"https://[a-z0-9.-]*cdninstagram\.com/v/t51\.[0-9-]+/[^\"'\s<>]+")
PROFILE_DIR = re.compile(r"/t51\.2885-19/")
SIZE_HINT = re.compile(r"[sp]\d{2,3}x\d{2,3}")


def _merge(found: list[str], markup: str) -> list[str]:
    seen: set[str] = set()
    out: list[str] = []
    for src in found + [html.unescape(m) for m in MARKUP_IMAGE.findall(markup)]:
        if PROFILE_DIR.search(src) or SIZE_HINT.search(src):
            continue
        key = src.split("?")[0].rsplit("/", 1)[-1]
        if not key or key in seen:
            continue
        seen.add(key)
        out.append(src)
    return out


async def _collect(embed_url: str) -> list[str]:
    async with AsyncCamoufox(headless=True) as browser:
        page = await browser.new_page()
        await page.goto(embed_url, wait_until="networkidle", timeout=TIMEOUT_MS)
        # The slider and lazy images settle a beat after the network goes quiet.
        await page.wait_for_timeout(1500)
        # strip(): Playwright only treats the string as a function to call when
        # it starts with the arrow, not with a newline.
        found = await page.evaluate(COLLECT_IMAGES.strip())
        markup = await page.content()
        return _merge([str(src) for src in (found or []) if isinstance(src, str)], markup)
