"""Image rendition sidecar for the CMS.

The media library stores what photographers upload, which since 2023 is mostly
straight off the camera: 6000px, 5-80MB JPEGs. The public site was putting those
originals into 400px cards. WordPress used to hide this by generating resized
copies on upload; the CMS does not, so this service is that step.

It is deliberately stateless, like the embedding sidecar. The CMS sends the
original's bytes and gets one encoded rendition back; it owns the files, the
database rows and the decision about what to render. If this service restarts
or is missing entirely, the site keeps serving originals.
"""

from __future__ import annotations

import logging
import os
import threading

import pyvips
from fastapi import FastAPI, HTTPException, Query, Request, Response
from starlette.concurrency import run_in_threadpool

# The recipe names everything that decides what a rendition looks like: format,
# quality, and the width ladder. The CMS stores it with each rendition and puts
# it in the file path, so changing any of those means changing this string. That
# is what makes the CMS re-render the library rather than keep serving stale
# files, and what keeps Cloudflare's 30-day immutable cache from pinning the old
# bytes under an unchanged URL.
RECIPE = os.getenv("IMAGE_RECIPE", "webp-q80-v1")
QUALITY = int(os.getenv("IMAGE_QUALITY", "80"))

# Ascending. 480/960 cover phone cards at 1x/2x, 1600 the article lead on a
# laptop, 2400 a full-bleed lead on a large or high-DPI screen. Nothing on the
# site is displayed wider than that.
WIDTHS = [int(w) for w in os.getenv("IMAGE_WIDTHS", "480,960,1600,2400").split(",")]

# Bounds on what one request can make this process allocate. The upload cap is
# 90MB, so the byte limit only has to clear that. The pixel limit is the real
# guard: a small PNG can declare a huge canvas, and decoding allocates by pixel
# count, not file size. 120MP is above anything a current camera produces.
MAX_BYTES = int(os.getenv("IMAGE_MAX_BYTES", str(100 * 1024 * 1024)))
MAX_PIXELS = int(os.getenv("IMAGE_MAX_PIXELS", str(120_000_000)))

# libvips already uses every core it is given for a single image, so rendering
# two at once only doubles peak memory. The CMS sends one at a time anyway; this
# holds if something else ever doesn't.
_render_lock = threading.Semaphore(int(os.getenv("IMAGE_MAX_CONCURRENT", "1")))

logger = logging.getLogger("imaging")

app = FastAPI(title="Triangle CMS imaging")


@app.get("/health")
def health() -> dict[str, object]:
    return {
        "status": "ok",
        "recipe": RECIPE,
        "format": "webp",
        "widths": WIDTHS,
        "libvips": f"{pyvips.version(0)}.{pyvips.version(1)}.{pyvips.version(2)}",
    }


class Unprocessable(Exception):
    """The input itself is the problem; retrying the same bytes will not help."""


def _render(data: bytes, width: int) -> tuple[bytes, int, int]:
    try:
        # Header only: new_from_buffer does not decode pixels until asked, so
        # this is how the canvas size is checked before anything is allocated.
        probe = pyvips.Image.new_from_buffer(data, "")
    except pyvips.Error as exc:
        raise Unprocessable(f"not a decodable image: {exc}") from exc
    if probe.width * probe.height > MAX_PIXELS:
        raise Unprocessable(f"{probe.width}x{probe.height} exceeds {MAX_PIXELS} pixels")

    try:
        # thumbnail_buffer is the libvips fast path. For JPEG it decodes at a
        # reduced scale directly (shrink-on-load), so a 6000px original costs
        # about what a 1500px one would. It also applies the EXIF orientation,
        # which phone photos depend on, and converts any embedded profile (Adobe
        # RGB, CMYK) to sRGB, without which colours shift visibly in browsers.
        #
        # size="down" never enlarges, so asking for 2400 from a 1200px original
        # returns 1200. The CMS reads the width it got back rather than assuming.
        image = pyvips.Image.thumbnail_buffer(
            data,
            width,
            height=10_000_000,
            size="down",
            export_profile="srgb",
        )
        # 16-bit PNGs come out of thumbnail as 16-bit; WebP is 8-bit only.
        if image.format != "uchar":
            image = image.colourspace("srgb")
        # keep="none" drops EXIF, XMP and ICC. The originals are served with
        # their EXIF intact, GPS included; the renditions should not be.
        encoded = image.webpsave_buffer(Q=QUALITY, effort=4, keep="none")
    except pyvips.Error as exc:
        raise Unprocessable(f"render failed: {exc}") from exc

    return encoded, image.width, image.height


@app.post("/render")
async def render(request: Request, width: int = Query(gt=0, le=10_000)) -> Response:
    declared = request.headers.get("content-length")
    if declared is not None and declared.isdigit() and int(declared) > MAX_BYTES:
        raise HTTPException(status_code=413, detail=f"body exceeds {MAX_BYTES} bytes")
    data = await request.body()
    if not data:
        raise HTTPException(status_code=400, detail="empty body")
    if len(data) > MAX_BYTES:
        raise HTTPException(status_code=413, detail=f"body exceeds {MAX_BYTES} bytes")

    def work() -> tuple[bytes, int, int]:
        with _render_lock:
            return _render(data, width)

    try:
        encoded, out_width, out_height = await run_in_threadpool(work)
    except Unprocessable as exc:
        # 422 tells the CMS to record this file as failed rather than retry it
        # on every pass forever.
        raise HTTPException(status_code=422, detail=str(exc)) from exc

    return Response(
        content=encoded,
        media_type="image/webp",
        headers={
            "X-Image-Width": str(out_width),
            "X-Image-Height": str(out_height),
            "X-Image-Recipe": RECIPE,
        },
    )
