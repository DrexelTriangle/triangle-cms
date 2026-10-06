# Article embeds: audio player and media embeds

Date: 2026-10-06
Status: draft for review
Repos: triangle-cms (server and frontend), Scalene
Requested by: Sachin, "Quizzes and Snippets" task list, item "audio player" and "better multimedia support (podcasts, video interviews, etc)"

## 1. Goal

Let a writer put a podcast episode, a video, or a social post into an article from the admin editor, and have it play on thetriangle.org. This is the first of three sub-projects cut from Sachin's list. Quizzes, timelines and charts are separate specs.

Two kinds of media are in scope:

- Link embeds. The writer pastes a share URL from YouTube, SoundCloud, Spotify, TikTok or Instagram.
- Uploaded audio. The writer uploads an MP3 to the CMS media library and places it in the article.

Video upload is out of scope. Video embeds come from YouTube only.

## 2. What exists today

Verified against main in both repos on 2026-10-05.

- Article bodies are one HTML string in the `text` column (`server/internal/database/http_models.go:91`). There is no block model.
- Scalene already expands two shortcodes, `[youtube URL]` and `[soundcloud url=... /]`, into styled player boxes (`src/utils/shortcodes.ts:145-156`, `src/styles/global.css:230-251`). Nothing else expands. Unknown shortcodes stay visible as text.
- The editor has no way to insert either one. The Trix toolbar has text, block and image tools only (`frontend/src/components/TrixEditor.tsx:1440-1657`).
- The CMS upload accepts jpeg, png, gif and webp, chosen by sniffing the first 512 bytes (`server/internal/handlers/media.go:83-88`, `:241-246`). No audio or video can be uploaded.
- The WordPress export (`wp-posts.xml`, 2026-05-06) has 223 `[youtube]` and 46 `[soundcloud]` shortcodes. It has no `[audio ...]` shortcode with attributes; the six `[audio` hits are bracketed prose such as `[audio archives]`. `[Spotify]`, `[TikTok]` and `[instagram]` appear 12, 18 and 14 times as bare words in prose, never as shortcodes.
- Scalene has no UI framework and no islands. Interactivity is plain `<script>` tags (`astro.config.mjs:123-127`). One script-dependent embed already exists: AmuseLabs puzzles, whose script is injected only when the body contains the marker div (`src/pages/[sections]/[article].astro:256, 308-313`).
- Scalene strips width and height out of every inline style in the body (`[article].astro:117-128`). Embeds must size themselves with classes.

## 3. Decisions

| Question | Decision | Reason |
|---|---|---|
| Audio source | Embed from platforms and upload MP3 | Editors asked for a podcast player that does not depend on a third party |
| Video source | Embed only | Hosting video means transcoding and bandwidth; YouTube already renders |
| Storage format | Shortcodes in the body HTML | Matches the migrated corpus; player markup can change without touching stored articles |
| Platforms | YouTube, SoundCloud, Spotify, TikTok, Instagram | YouTube and SoundCloud already render; the rest were requested |
| Resolution | Client-side URL parser, no oEmbed | For these providers the embed URL derives from the share URL with a regex; no network call or API key needed |

Approaches considered and rejected:

- Server-side oEmbed resolver. Gives titles and thumbnails in the editor and resolves short links, at the cost of a Go handler with outbound HTTP and, for Instagram, a Meta app token plus App Review. A possible later layer; the shortcode format does not change if it is added.
- Raw HTML block. Writers paste provider embed code. Stores unsanitized iframes and scripts from anyone with editor access. Rejected.

## 4. The shortcode contract

This is the interface between the two repos. The editor writes it, Scalene reads it, and the ETL corpus already contains the first two rows.

| Shortcode | Status | Example |
|---|---|---|
| `[youtube URL]` | legacy, now also written by the editor | `[youtube https://www.youtube.com/watch?v=abc123]` |
| `[soundcloud url="..." /]` | legacy, now also written by the editor | `[soundcloud url="https://soundcloud.com/user/track" /]` |
| `[spotify url="..." /]` | new | `[spotify url="https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5" /]` |
| `[tiktok url="..." /]` | new | `[tiktok url="https://www.tiktok.com/@user/video/6718335390845095173" /]` |
| `[instagram url="..." /]` | new | `[instagram url="https://www.instagram.com/p/Cabc123/" /]` |
| `[audio src="..." /]` | new | `[audio src="<MEDIA_BASE_URL>/wp-content/uploads/2026/10/ep1.mp3" /]` |

Rules:

- New shortcodes store the share URL the writer pasted, never a derived embed URL or a bare ID. Scalene derives the ID at render time. If a provider changes its player URL, only Scalene changes.
- New shortcodes require the `url=` (or `src=`) attribute. `[Spotify]` on its own is prose and stays as text.
- A shortcode sits in its own top-level block, matching how `[youtube]` sits in the corpus. The editor guarantees this because a Trix attachment is always its own block.
- A malformed shortcode stays visible as text. Nothing is dropped silently. This is the existing behavior at `shortcodes.ts:141-143`.
- No caption attribute. Writers put context in the paragraph above.

The audio example host is illustrative. The real host is whatever `MEDIA_BASE_URL` resolves to, and the path shape comes from `media.go:254-256`.

## 5. Scalene rendering

Four new functions in `src/utils/shortcodes.ts`, each shaped like the existing `soundcloudEmbed`: parse attributes, validate, return the shortcode text unchanged on any failure, size by class.

| Shortcode | Validation | Output |
|---|---|---|
| `[spotify url=]` | host `open.spotify.com`, path `/(track\|episode\|show\|album\|playlist)/([A-Za-z0-9]+)` | `.article-embed.article-embed-audio` wrapping an iframe at `open.spotify.com/embed/{type}/{id}`; height 152 for episode and track, 352 otherwise |
| `[tiktok url=]` | host `tiktok.com` or `www.tiktok.com`, path `/@[^/]+/video/(\d+)` | `.article-embed.article-embed-video.article-embed-portrait` wrapping an iframe at `www.tiktok.com/player/v1/{id}?description=1` |
| `[instagram url=]` | host `instagram.com` or `www.instagram.com`, path `/(p\|reel)/([A-Za-z0-9_-]+)/?` | `.article-embed.article-embed-social` wrapping the official `<blockquote class="instagram-media" data-instgrm-permalink="...">` with a plain `<a>` to the post inside as fallback |
| `[audio src=]` | absolute URL, extension `.mp3` | `.article-embed.article-embed-audio` wrapping `<audio controls preload="metadata" src="...">` |

Sources for the embed URL patterns: Spotify, developer.spotify.com/documentation/embeds/tutorials/creating-an-embed. TikTok, developers.tiktok.com/doc/embed-player. Instagram, developers.facebook.com/docs/instagram-platform/oembed. Instagram has no documented script-free form; the `/embed/` iframe some sites use is undocumented and is not used here.

Instagram script: a boolean `hasInstagramEmbed = content.includes("instagram-media")` in `[article].astro`, and a conditional `<script is:inline async src="https://www.instagram.com/embed.js">` next to the AmuseLabs script at lines 308-313. If the script does not load, the reader sees the plain link. No `integrity` hash: Instagram rotates embed.js without versioned URLs, so a pinned hash would break the embed on their next deploy. This matches how the AmuseLabs script is loaded today.

CSS in `global.css`, next to lines 230-251:

- `.article-embed-portrait`: aspect ratio 9 / 16, max width 325px, centered. TikTok's player is vertical and the existing 16:9 box would letterbox it.
- `.article-embed-social`: no fixed aspect ratio. Instagram sizes its own blockquote.

Guard rails that already apply: every size is a class because of the dimension stripper; the paragraph splitter in `articleHtml.ts:39` does not split inside a block that contains `div`, and every expander output starts with `<div>`.

Not done here: YouTube and SoundCloud rendering (already works), oEmbed calls, short links (`vm.tiktok.com`, `spotify.link`) which fail validation and stay as text.

## 6. CMS: accept MP3 uploads

The upload path takes a new type with almost no structural change.

1. Allowlist. Rename `allowedImageTypes` to `allowedUploadTypes` in `media.go` and add `"audio/mpeg": ".mp3"`. The sideload route `/v1/media/fetch` uses the same map, so remote MP3 URLs also work.
2. Sniff fallback. Go's `http.DetectContentType` returns `audio/mpeg` only when the file starts with an ID3 tag. Checked on go1.26.4 with synthetic headers: ID3 gives `audio/mpeg`; a bare MPEG frame and an M4A both give `application/octet-stream`. After the sniff, one extra branch: if the result is `application/octet-stream`, the client filename ends in `.mp3`, and the first two bytes are an MPEG frame sync (`0xFF`, then high three bits of the next byte set), treat it as `audio/mpeg`. No library. M4A and WAV stay rejected.
3. Indexer. Add `".mp3": "audio/mpeg"` to `mediaMimeByExt` (`database/media.go:38-45`) so a reindex picks up audio that was rsynced rather than uploaded.
4. Size limit. Keep 90 MiB. A one-hour podcast at 128 kbps is about 58 MB.
5. Dimensions. `imageDimensions` returns 0, 0 for a file it cannot decode (`media.go:668-680`). Audio rows store width and height as 0. No schema change; `media` already has `mime_type` and `size_bytes`.
6. Renditions. `MediaNeedingRenditions` selects only `image/jpeg`, `image/png` and `image/webp` (`database/media_renditions.go:64-79`). Audio rows never reach the libvips sidecar.
7. Media library. Extend the picker's `accept` list at `frontend/src/components/MediaPicker.tsx:239` with `audio/mpeg`. A library item whose `mime_type` starts with `audio/` renders a generic audio tile instead of an `<img>`.

Not done here: transcoding, waveform, duration extraction, M4A or WAV, a separate audio library view.

## 7. The editor

Two block kinds, one toolbar button, two serialization branches. Trix 2.1.19 has a built-in content attachment: an attachment constructed with a `content` HTML string renders that HTML inside its figure and serializes it into the `data-trix-attachment` JSON, so it survives a reload with no upload.

### Embed block

Chain: Embed button in the toolbar, inline URL prompt, parser, content attachment.

- `frontend/src/lib/embedUrl.ts`, new, pure functions, no DOM.
  - `parseEmbedUrl(url): { provider, url } | null`. Providers: `youtube`, `soundcloud`, `spotify`, `tiktok`, `instagram`. Matched by host plus the same path regexes Scalene uses in section 5, so both sides agree on what is valid. Short links return `null`.
  - `embedToShortcode(embed): string` and `shortcodeToEmbed(text): embed | null`, inverses.
- Attachment: `new Trix.Attachment({ content, contentType: "application/x-triangle-embed", embedProvider, embedUrl })`. `content` is a static preview card: provider name, the URL, and a line saying the player renders on the live site. No iframe in the editor, so no third-party script runs in the admin app.
- `embedProvider` and `embedUrl` are pushed onto `AttachmentPiece.permittedAttributes` the same way `align` is at `TrixEditor.tsx:41-43`, so they survive a re-render. `TrixAttachmentAttributes` in `frontend/src/trix.d.ts` gains the two fields plus `content`.
- The URL prompt reuses the toolbar's existing link-prompt pattern. No modal library. On a `null` parse it shows one line naming the five accepted hosts.

### Audio block

No new insertion path. `trix-file-accept` (`TrixEditor.tsx:451-466`) and `uploadFile` (`:260`) already run for any dropped or chosen file.

- `trix-file-accept` gains a check: the file type must be an accepted image type or `audio/mpeg`, else prevent default and show a notice. The server already enforces this; the client check only saves a round trip.
- After `POST /v1/media` returns, the handler sets `url` and `contentType` on the attachment as it does now. Trix renders a non-previewable attachment as a filename chip, which is the editor view for audio.
- Inserting an audio row from the picker builds an attachment with `contentType: "audio/mpeg"`.

### Serialization

`frontend/src/components/trixImageHtml.ts` is renamed `trixArticleHtml.ts` because it no longer only handles images. Imports update; nothing else moves.

- Save, `trixHtmlToArticle`. Line 236 currently skips every non-image attachment. Replace the skip with a branch:
  - `contentType === "application/x-triangle-embed"`: emit the shortcode from `embedProvider` and `embedUrl` as the text of its own `<div>`.
  - `contentType === "audio/mpeg"` and `url` not `blob:` or `data:`: emit `[audio src="URL" /]` in its own `<div>`.
  - Anything else passes through unchanged, as today.
- Load, `articleHtmlToTrix`. A new pass before the figure pass. For each top-level block, or bare top-level text node as the legacy corpus has them, whose text is exactly one shortcode that `shortcodeToEmbed` recognizes, or an `[audio src=]`, replace it with `<figure data-trix-attachment='{...}'>` carrying the attributes the insert path would produce. The 223 legacy `[youtube]` articles therefore open as embed cards instead of raw text.

### Error handling

- Parse failure: one-line notice in the prompt, nothing inserted.
- Upload failure: the existing `failAttachment` path removes the chip and shows the server message.
- An embed figure with missing `embedProvider` or `embedUrl` falls through to pass-through. Nothing is deleted silently.

Not done here: live player in the editor, thumbnails or titles, captions on embeds, changes to drag-reorder (the existing overlay handles any figure).

## 8. Tests

### triangle-cms server (Go, stdlib testing)

- `media_test.go`: an ID3-tagged MP3 is stored as `audio/mpeg` with `.mp3`; a bare frame-sync MP3 named `.mp3` is accepted; the same bytes named `.bin` are rejected; an M4A is rejected; a PNG still works. Fixtures are a few hundred synthetic bytes.
- `media_renditions_test.go`: an `audio/mpeg` row is never returned by `MediaNeedingRenditions`.

### triangle-cms frontend (Vitest)

- `embedUrl.test.ts`: per provider, one valid URL and the rejected shapes (short link, wrong host, missing ID); round trip `shortcodeToEmbed(embedToShortcode(x))` equals `x`.
- `trixArticleHtml.test.ts`, new, since the module has no tests today: for each shortcode, load then save returns the identical string; an embed figure with missing attributes passes through; an `audio/mpeg` attachment on a `blob:` URL is left alone; the image cases from `editArticleView.test.tsx` that touch serialization still pass.

### Scalene

The repo has no test runner. Add Vitest (Node environment), a `test` script, and a `vitest run` step in `.github/workflows/ci.yml` beside `astro check`. One file, `src/utils/shortcodes.test.ts`: each expander on a valid input, each on malformed input returning the text unchanged, and `[Spotify]` with no `url=` left as text. This is the first test file in Scalene; say so in the PR.

### Manual

In the admin editor, insert each of the five embeds plus one MP3, save, reload, confirm the cards return. Open the article on the local Scalene dev server and confirm each player renders. Repeat on dev.thetriangle.org after merge. If an embed is blank in production, check the response headers for a Content-Security-Policy before filing a bug; nginx or Cloudflare may add one that Scalene's middleware does not.

## 9. Delivery

Three pull requests, in dependency order. Each is safe to merge alone because nothing downstream reads the new capability until the last one.

1. triangle-cms server: allowlist, sniff fallback, indexer, Go tests.
2. Scalene: four expanders, Instagram script, CSS, Vitest setup and tests. Inert until something writes the shortcodes.
3. triangle-cms frontend: `embedUrl.ts`, serialization branches, toolbar button, picker changes, Vitest tests. Needs PR 1 for audio to work end to end.

## 10. Out of scope

Video upload. oEmbed, thumbnails and titles. Short links. Captions on embeds. M4A and WAV. A dedicated audio library view. Quizzes, timelines and charts, which are the next two sub-projects.
