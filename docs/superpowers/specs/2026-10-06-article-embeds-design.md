# Article embeds: audio player and media embeds

Date: 2026-10-06, revised the same day after council review
Status: draft for review
Repos: triangle-cms (server and frontend), Scalene
Requested by: Sachin, "Quizzes and Snippets" task list, item "audio player" and "better multimedia support (podcasts, video interviews, etc)"

## 1. Goal

Let a writer put a podcast episode, a video, or a social post into an article from the admin editor, and have it play on thetriangle.org. This is the first of three sub-projects cut from Sachin's list. Quizzes, timelines and charts are separate specs.

Two kinds of media are in scope:

- Link embeds. The writer pastes a share URL from YouTube, SoundCloud, Spotify, TikTok or Instagram.
- Uploaded audio. The writer uploads an MP3 to the CMS media library and places it in the article.

No uploaded video. Provider-hosted video comes from YouTube, TikTok and Instagram.

## 2. What exists today

Verified against main in both repos on 2026-10-05 and 2026-10-06.

- Article bodies are one HTML string in the `text` column (`server/internal/database/http_models.go:91`). There is no block model.
- Scalene already expands two shortcodes, `[youtube URL]` and `[soundcloud url=... /]`, into styled player boxes (`src/utils/shortcodes.ts:145-156`, `src/styles/global.css:230-251`). The expander is a regex over the whole body string. Unknown shortcodes stay visible as text.
- The editor has no way to insert either one. The Trix toolbar has text, block and image tools only (`frontend/src/components/TrixEditor.tsx:1440-1657`).
- The editor already forces an image into its own block: `openBlockForImage` (`TrixEditor.tsx:386-398`) inserts a block break when the caret's block is not empty. Embeds and audio reuse it.
- The CMS upload accepts jpeg, png, gif and webp, chosen by sniffing the first 512 bytes (`server/internal/handlers/media.go:83-88`, `:241-246`). No audio or video can be uploaded.
- The sideload route `/v1/media/fetch` already refuses non-public addresses on every hop (`safeDialControl`, `media.go:436-452`), caps redirects at 5, caps the body with `io.LimitReader` at the upload limit (`:404`), and times out at 30 s (`:62`).
- The indexer lowercases extensions before the MIME lookup (`database/media.go:447-448`).
- The WordPress export (`wp-posts.xml`, 2026-05-06) has 223 `[youtube]` and 46 `[soundcloud]` shortcodes. Measured placement: 241 sit alone on their line (162 inside `<p>`, 79 as bare text), 5 sit inline with prose, none share a line with another shortcode. YouTube shapes: 181 `watch?v=`, 21 `watch?v=` plus `&w=560&h=315`, 18 bare `[youtube]`, and 20 odd strings such as `[youtube: Marc Zapanta]`. All 43 SoundCloud URLs point at `api.soundcloud.com/tracks/...` and carry up to 13 attributes. There is no `[audio ...]` shortcode; the six `[audio` hits are prose such as `[audio archives]`. `[Spotify]`, `[TikTok]` and `[instagram]` appear 12, 18 and 14 times as bare words in prose.
- Scalene has no UI framework and no islands. Interactivity is plain `<script>` tags (`astro.config.mjs:123-127`). Article pages use `ArticleLayout`, which does not include Astro's `ClientRouter`; only `BaseLayout` does (`src/layouts/BaseLayout.astro:2,110`). So inline scripts on article pages run on every full load and there is no view-transition re-run problem.
- One script-dependent embed already exists: AmuseLabs puzzles, whose script is injected only when the expanded body contains the marker div (`src/pages/[sections]/[article].astro:256, 308-313`). The `content` variable there is already expanded (line 108), so a marker check on it sees expander output.
- Scalene strips width and height out of every inline style in the body (`[article].astro:117-128`). Attributes such as `height="300"` on an iframe survive; the existing SoundCloud expander relies on that.
- The nginx config is not in either repo. `deploy/nginx/` holds only an upstreams example. Media response headers, body size limit and Range support are server-side checks.

## 3. Decisions

| Question | Decision | Reason |
|---|---|---|
| Audio source | Embed from platforms and upload MP3 | Editors asked for a podcast player that does not depend on a third party |
| Video source | Embed only | Hosting video means transcoding and bandwidth; YouTube already renders |
| Storage format | Shortcodes in the body HTML | Matches the migrated corpus; player markup can change without touching stored articles |
| Platforms | YouTube, SoundCloud, Spotify, TikTok, Instagram | YouTube and SoundCloud already render; the rest were requested |
| Resolution | Client-side URL parser, no oEmbed | For these providers the embed URL derives from the share URL with a regex; no network call or API key needed |
| Instagram script | Accepted | Instagram has no documented script-free embed. Eden accepts `embed.js` running in the page, the same trust already given to AmuseLabs. Recorded here as a deliberate decision, not an inherited one |
| YouTube cookies | Unchanged | `youtube-nocookie.com` would change all 223 legacy articles, which Sachin did not ask for |

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

- The editor stores a canonical URL rebuilt from the validated host and captured ID, never the raw string the writer pasted. Tracking parameters such as `?igsh=` and locale prefixes such as `/intl-de/` are dropped. SoundCloud is the exception: its ID is not in the URL, so the share URL is stored after validation and encoding (see the grammar below).
- Attribute grammar. Inside `url="..."` and `src="..."` the value contains only ASCII letters, digits and `-._~:/?#@!$&'()*+,;=%`. The editor percent-encodes anything else, including `"`, `]`, `<`, `>` and whitespace. Scalene parses the value as `[^"]*` and HTML-escapes it on output. Canonical URLs built from IDs already satisfy this; only SoundCloud needs the encoder.
- New shortcodes require the `url=` or `src=` attribute. `[Spotify]` on its own is prose and stays as text.
- A shortcode is a block of its own. The editor guarantees this by calling `openBlockForImage` before every insert, the same way images do.
- A malformed shortcode stays visible as text. Nothing is dropped silently. This is the existing behavior at `shortcodes.ts:141-143`.
- No caption attribute. Writers put context in the paragraph above.

The audio example host is illustrative. The real host is whatever `MEDIA_BASE_URL` resolves to, and the path shape comes from `media.go:254-256`. Images already store absolute URLs built from the same variable (`media.go:148`), so audio shares their host-migration risk rather than adding a new one.

### Accepted inputs and canonical forms

Both repos ship the same test-vector file, `embed-vectors.json`, with accept and reject cases per provider. The frontend copy is the source; the Scalene copy is checked in beside it and CI in each repo runs its parser against its copy. A drift between them fails the Scalene build, which is the side readers see.

| Provider | Accepted paste shapes | Canonical stored URL |
|---|---|---|
| YouTube | `youtube.com/watch?v=ID`, `m.youtube.com/watch?v=ID`, `youtu.be/ID`, `youtube.com/shorts/ID`; ID matches `^[\w-]{11}$` | `https://www.youtube.com/watch?v=ID` |
| SoundCloud | host `soundcloud.com`, `www.soundcloud.com`, `on.soundcloud.com` or `api.soundcloud.com`, `https` only | the share URL, encoded per the grammar |
| Spotify | host `open.spotify.com`, optional `/intl-xx/` prefix, path `^/(track\|episode\|show\|album\|playlist)/([A-Za-z0-9]{22})/?$` | `https://open.spotify.com/{type}/{id}` |
| TikTok | host `tiktok.com`, `www.tiktok.com` or `m.tiktok.com`, path `^/@([\w.-]+)/video/(\d+)/?$` | `https://www.tiktok.com/@{user}/video/{id}` |
| Instagram | host `instagram.com` or `www.instagram.com`, path `^/(p\|reel\|reels)/([A-Za-z0-9_-]+)/?$`; `reels` maps to `reel` | `https://www.instagram.com/{p\|reel}/{id}/` |

Every path regex is anchored and runs against `new URL(input).pathname`, never the raw string. Known short-link hosts (`vm.tiktok.com`, `vt.tiktok.com`, `spotify.link`) are detected by host and produce a specific message: open the link in a browser and paste the full URL. Any other failure produces the generic message naming the five accepted hosts.

## 5. Scalene rendering

Four new functions in `src/utils/shortcodes.ts`, each shaped like the existing `soundcloudEmbed`: parse attributes, validate against the same rules as section 4, rebuild the output URL from the captured ID, return the shortcode text unchanged on any failure, size by class or attribute.

| Shortcode | Output |
|---|---|
| `[spotify url=]` | `.article-embed.article-embed-audio` wrapping an iframe at `https://open.spotify.com/embed/{type}/{id}` with `height="152"` for episode and track, `height="352"` otherwise, `allow="encrypted-media"`, `loading="lazy"`, `title="Spotify player"` |
| `[tiktok url=]` | `.article-embed.article-embed-video.article-embed-portrait` wrapping an iframe at `https://www.tiktok.com/player/v1/{id}` with `loading="lazy"`, `title="TikTok video"` |
| `[instagram url=]` | `.article-embed.article-embed-social` wrapping the official `<blockquote class="instagram-media" data-instgrm-permalink="{canonical}">` with a plain `<a href="{canonical}">View this post on Instagram</a>` inside as fallback |
| `[audio src=]` | `.article-embed.article-embed-audio` wrapping `<audio controls preload="metadata" src="{src}" aria-label="Audio player">` followed by `<a href="{src}" download>Download MP3</a>` |

Audio `src` validation: parses as a URL, scheme `https`, host equal to the host of `MEDIA_BASE_URL`, and `pathname` ends in `.mp3`. Anything else stays as text.

Every attribute value is passed through the existing `escapeAttribute`. Output URLs are built from the validated parts, so a writer's query string never reaches the page.

Where the expander runs. The expander is a regex over the whole body, so the new patterns match only when the shortcode starts at the beginning of the string, after `>`, or after a newline plus optional whitespace, and ends at the end of the string, before `<`, or before a newline. That excludes a shortcode inside an attribute value or inside a sentence. Before matching, `<pre>` and `<code>` ranges are masked so a code sample that shows a shortcode is left alone. Tests cover both.

Sources for the embed URL patterns: Spotify, developer.spotify.com/documentation/embeds/tutorials/creating-an-embed. TikTok, developers.tiktok.com/doc/embed-player. Instagram, developers.facebook.com/docs/instagram-platform/oembed. Instagram has no documented script-free form; the `/embed/` iframe some sites use is undocumented and is not used here.

TikTok's player query. The embed-player docs offer `description=1` and `music_info=1`. Whether the player then exceeds a 9:16 box is checked in a browser during the Scalene PR; if it clips, the parameters are dropped rather than the aspect ratio changed.

Instagram script: `hasInstagramEmbed = content.includes("instagram-media")` in `[article].astro`, computed on the expanded `content` exactly like `hasPuzzleScript`, and a conditional `<script is:inline async src="https://www.instagram.com/embed.js">` next to the AmuseLabs script at lines 308-313. No `integrity` hash: Instagram rotates embed.js without versioned URLs, so a pinned hash would break the embed on their next deploy. If the script does not load, the reader sees the plain link.

CSS in `global.css`, next to lines 230-251:

- `.article-embed iframe { width: 100%; border: 0 }` as a base rule, so a height attribute sets the height and the box fills its column.
- `.article-embed-portrait`: aspect ratio 9 / 16, max width 325px, centered. TikTok's player is vertical and the existing 16:9 box would letterbox it.
- `.article-embed-social`: no fixed aspect ratio. Instagram sizes its own blockquote.

Pipeline order, so the splitter argument holds: `expandShortcodes` runs first (`[article].astro:108`), then `normalizeArticleHtml`. By the time the paragraph splitter sees the body, every shortcode is already a `<div>`, and `div` is in the splitter's skip list (`articleHtml.ts:39`). A test places a bare legacy shortcode between two paragraphs and checks the output.

Not done here: YouTube and SoundCloud rendering (already works), oEmbed calls, short links.

## 6. CMS: accept MP3 uploads

1. Allowlist. Rename `allowedImageTypes` to `allowedUploadTypes` in `media.go` and add `"audio/mpeg": ".mp3"`. The sideload route uses the same map and already has the protections listed in section 2, so remote MP3 URLs also work. The sideload filename comes from the URL path with the query removed.
2. Sniff fallback. Go's `http.DetectContentType` returns `audio/mpeg` only when the file starts with an ID3 tag. Checked on go1.26.4 with synthetic headers: ID3 gives `audio/mpeg`; a bare MPEG frame and an M4A both give `application/octet-stream`. After the sniff, one extra branch: if the result is `application/octet-stream` and the client filename ends in `.mp3`, validate a full 4-byte MPEG audio frame header: 11 sync bits set, version bits not `01`, layer bits not `00`, bitrate index not `1111`, sample-rate index not `11`. If it passes, treat the file as `audio/mpeg`. The 11-bit sync alone would also admit AAC ADTS frames. Tests use real MP3 fixtures of a few kilobytes as well as synthetic headers. M4A and WAV stay rejected.
3. Indexer. Add `".mp3": "audio/mpeg"` to `mediaMimeByExt` (`database/media.go:38-45`). A reindex then imports every MP3 already under `wp-content/uploads`. Before merging, count them on the server with `find $MEDIA_ROOT/wp-content/uploads -iname '*.mp3' | wc -l` and confirm the import is wanted.
4. Size limit. Keep 90 MiB. A one-hour podcast at 128 kbps is about 58 MB.
5. Dimensions. `imageDimensions` returns 0, 0 for a file it cannot decode (`media.go:668-680`). Audio rows store width and height as 0. No schema change; `media` already has `mime_type` and `size_bytes`.
6. Renditions. `MediaNeedingRenditions` selects only `image/jpeg`, `image/png` and `image/webp` (`database/media_renditions.go:64-79`). Audio rows never reach the libvips sidecar.
7. Image-only consumers. Every place that lists media and expects an image gets a filter on `mime_type LIKE 'image/%'`: the featured-image picker, gallery inserts, image replacement, and any og:image lookup. The implementation plan enumerates them from `rg -n 'ListMedia|/v1/media' frontend/src server/internal`.
8. Media library. Extend the picker's `accept` list at `frontend/src/components/MediaPicker.tsx:239` with `audio/mpeg,.mp3`. A library item whose `mime_type` starts with `audio/` renders a generic audio tile instead of an `<img>`.

Server-side checks before the CMS PR deploys, recorded in the PR:

- A real 60 MB MP3 pushed through Cloudflare, nginx and Go. nginx `client_max_body_size` and Go timeouts must clear it.
- `curl -sI -H 'Range: bytes=0-1' <media url>` returns `206` with `Content-Type: audio/mpeg`, so seeking works.
- The media origin sends `X-Content-Type-Options: nosniff`, so a polyglot file that passed the frame check is never interpreted as HTML by a browser.

Not done here: transcoding, waveform, duration extraction, M4A or WAV, a separate audio library view.

## 7. The editor

Two block kinds, one toolbar button, two serialization branches. Trix 2.1.19 has a built-in content attachment: an attachment constructed with a `content` HTML string renders that HTML inside its figure and serializes it into the `data-trix-attachment` JSON, so it survives a reload with no upload.

### Embed block

Chain: Embed button in the toolbar, inline URL prompt, parser, `openBlockForImage`, content attachment.

- `frontend/src/lib/embedUrl.ts`, new, pure functions, no DOM.
  - `parseEmbedUrl(input): { provider, url } | null`, implementing section 4. `url` is the canonical form. A short-link host returns a distinct error so the prompt can give the specific message.
  - `embedToShortcode(embed): string`, with a per-provider template. YouTube emits the bare form `[youtube URL]`; the other four emit `[name url="URL" /]`. One template per provider, each pinned by a test.
  - `shortcodeToEmbed(text): embed | null`, the inverse for canonical strings. For legacy strings it returns the parsed embed plus the exact original text.
- Attachment: `new Trix.Attachment({ content, contentType: "application/x-triangle-embed", embedProvider, embedUrl })`. `content` is a static preview card built with DOM APIs and `textContent`: provider name, the canonical URL, and a line saying the player renders on the live site. No iframe in the editor, so no third-party script runs in the admin app.
- `embedProvider`, `embedUrl`, `embedOriginal` and `embedWrapper` are pushed onto `AttachmentPiece.permittedAttributes` the same way `align` is at `TrixEditor.tsx:41-43`, so they survive a re-render. `TrixAttachmentAttributes` in `frontend/src/trix.d.ts` gains those fields plus `content`.
- The URL prompt reuses the toolbar's existing link-prompt pattern. No modal library.
- Trix's caption field is hidden for this content type and for audio with a CSS rule keyed on the figure's content-type attribute, so a caption cannot be typed and then dropped. The exact attribute Trix sets on the figure is confirmed at implementation; `data-trix-content-type` is the expected one.

### Audio block

No new insertion path. `trix-file-accept` (`TrixEditor.tsx:451-466`) and `uploadFile` (`:260`) already run for any dropped or chosen file.

- `trix-file-accept` gains a check. Accepted: an image type the server allows, any `audio/*` type, or an empty type when the filename ends in `.mp3`. Browsers report MP3s as `audio/mpeg`, `audio/mp3`, `audio/mpeg3` or nothing, so the client never decides on the type string alone. Anything else: prevent default and show a notice.
- After `POST /v1/media` returns, the handler sets `url` and sets `contentType` from the response's `content_type`, not from the browser's `File.type`. The server's sniffed type is the one the save branch keys on.
- Trix renders a non-previewable attachment as a filename chip, which is the editor view for audio.
- Inserting an audio row from the picker builds an attachment with `contentType` from the row's `mime_type`.

### Pending uploads

Today a save during an image upload leaves a `blob:` figure in the Trix markup, which the serializer skips. The same would happen to a 58 MB audio upload. Fix for both: the editor exposes `hasPendingUploads()`, counting attachments whose `url` starts with `blob:` or `data:`. `editArticleView` disables the save button and skips autosave while it returns true, and shows "Waiting for upload to finish". This changes image behavior too, and the PR says so.

### Serialization

`frontend/src/components/trixImageHtml.ts` becomes `trixArticleHtml.ts` because it no longer only handles images. The rename is its own commit with no behavior change, so the review diff shows only the new branches.

Current behavior, pinned by a test before anything changes: at line 236, a figure whose `contentType` is not an image is left exactly as Trix wrote it, `data-trix-attachment` and all. It is not deleted. Readers would see Trix's figure markup, which the public site does not style.

- Save, `trixHtmlToArticle`. The line 236 skip becomes a branch:
  - `contentType === "application/x-triangle-embed"`: re-run `parseEmbedUrl(embedUrl)`. If it fails, pass the figure through unchanged. If `embedOriginal` is set and the canonical URL equals the one parsed at load, emit `embedOriginal` verbatim inside its original wrapper (`p`, `div`, or bare text). Otherwise emit the canonical shortcode as the text of its own `<div>`.
  - `contentType === "audio/mpeg"`: validate `url` with the same rules as the Scalene audio check (https, media host, `.mp3` pathname). If it fails, including `blob:` and `data:`, pass through unchanged. Otherwise emit `[audio src="URL" /]` in its own `<div>`.
  - Anything else passes through unchanged, as today.
- Load, `articleHtmlToTrix`. A new pass before the figure pass. For each top-level `<p>`, `<div>` or bare text node whose trimmed text is exactly one shortcode that `shortcodeToEmbed` recognizes, or an `[audio src=]` that validates, replace it with a figure built by `createElement` and `setAttribute("data-trix-attachment", JSON.stringify(attrs))`. The attrs carry the canonical `embedUrl`, the exact `embedOriginal` text and `embedWrapper`. The preview card is rebuilt from the validated attrs; any `content` already present in a stored figure is discarded, never rendered. The 5 legacy shortcodes that sit inline with prose, the 18 bare `[youtube]` tags and the 20 odd strings fail the parse and stay as text in the editor.

### Error handling

- Parse failure: one-line notice in the prompt, nothing inserted.
- Upload failure: the existing `failAttachment` path removes the chip and shows the server message.
- An embed figure with missing or invalid `embedProvider` or `embedUrl` falls through to pass-through. Nothing is deleted silently.

Not done here: live player in the editor, thumbnails or titles, captions on embeds, changes to drag-reorder (the existing overlay handles any figure).

## 8. Tests

### triangle-cms server (Go, stdlib testing)

- `media_test.go`: an ID3-tagged MP3 is stored as `audio/mpeg` with `.mp3`; a bare-frame MP3 named `.mp3` is accepted; the same bytes named `.bin` are rejected; an AAC ADTS header named `.mp3` is rejected; an M4A is rejected; a PNG still works. Fixtures: two real MP3 files of a few kilobytes plus synthetic headers.
- `media_renditions_test.go`: an `audio/mpeg` row is never returned by `MediaNeedingRenditions`.

### triangle-cms frontend (Vitest)

- `embedUrl.test.ts`: every accept and reject vector in `embed-vectors.json`; hostile inputs (`"`, `]`, `javascript:`, `?x="><img onerror=...>`) rejected or encoded; `shortcodeToEmbed(embedToShortcode(x))` equals `x` for every provider.
- `trixArticleHtml.test.ts`, new, since the module has no tests today:
  - corpus round trip: every distinct `[youtube]` and `[soundcloud]` string from `wp-posts.xml` that parses, in each of its wrappers, loads and saves byte-identical. The distinct strings are extracted once into a fixture file.
  - canonical round trip per provider and for audio.
  - a figure with missing or invalid attributes passes through unchanged.
  - an `audio/mpeg` attachment on a `blob:` URL passes through unchanged.
  - a stored figure carrying hostile `content` loads with the card rebuilt and the stored HTML absent.
  - the image cases from `editArticleView.test.tsx` that touch serialization still pass.
- `editArticleView.test.tsx`, existing jsdom plus Trix harness, new cases: insert an embed with the caret mid-paragraph and check it lands in its own block; insert next to an image; undo and redo keep the attributes; copy and paste of an embed figure keeps them; picker insertion of an audio row; save is disabled while an upload is pending; the `permittedAttributes` push happens once.

### Scalene

The repo has no test runner. Add Vitest (Node environment), a `test` script, and a `vitest run` step in `.github/workflows/ci.yml` beside `astro check`. One file, `src/utils/shortcodes.test.ts`: every vector in the repo's copy of `embed-vectors.json`; each expander on malformed input returning the text unchanged; `[Spotify]` with no `url=` left as text; a shortcode inside `<code>` and inside an attribute value left as text; a bare legacy shortcode between two paragraphs survives `normalizeArticleHtml`; every emitted attribute value is escaped. This is the first test file in Scalene; say so in the PR.

### Manual

In the admin editor, insert each of the five embeds plus one MP3, save, reload, confirm the cards return. Open the article on the local Scalene dev server and confirm each player renders, including the TikTok box at phone width. Repeat on dev.thetriangle.org after merge.

## 9. Delivery

Two pull requests, Scalene first. The server allowlist ships in the same PR as the editor, so there is never a window where MP3s can be uploaded but not placed.

1. Scalene: four expanders, Instagram script, CSS, `embed-vectors.json`, Vitest setup and tests. Inert until something writes the shortcodes. Must be live in production before PR 2 deploys, or readers would see raw shortcode text.
2. triangle-cms: rename commit, then server allowlist and sniff fallback and indexer with Go tests, then `embedUrl.ts`, serialization branches, toolbar button, pending-upload guard, picker changes and Vitest tests. Deploys only after PR 1 is confirmed on www.thetriangle.org.

Before PR 1 deploys, audit the effective production Content-Security-Policy across Scalene middleware (none today), nginx and Cloudflare. Required additions, if a policy exists: `frame-src` for `www.youtube.com`, `w.soundcloud.com`, `open.spotify.com`, `www.tiktok.com`; `script-src` for `www.instagram.com` and the existing `puzzleme.amuselabs.com`. If no policy exists, note that in the PR and leave adding one out of scope.

Third parties that load on an article page after this change, for the record: YouTube, SoundCloud, Spotify, TikTok and Instagram iframes or scripts, each only when an article embeds them, plus the existing AdSense, Matomo and AmuseLabs. Each sets its own cookies on readers.

## 10. Out of scope

Video upload. oEmbed, thumbnails and titles. Short links. Captions on embeds. M4A and WAV. A dedicated audio library view. `youtube-nocookie.com`. A Content-Security-Policy where none exists today. Quizzes, timelines and charts, which are the next two sub-projects.

## 11. Council review, 2026-10-06

Three models reviewed the first draft. Findings and their disposition:

- Applied: anchored regexes on `pathname`, URLs rebuilt from IDs, an attribute grammar, `setAttribute` plus `JSON.stringify` for the Trix JSON, revalidation on save, https and media-host-only audio, preview cards rebuilt from attributes, lossless legacy round trip via `embedOriginal`, shared test vectors, per-provider templates, `contentType` from the server response, a pending-upload save guard, hidden caption field, a 4-byte MPEG header check, expander position rules and `<pre>`/`<code>` masking, iframe base CSS, accessibility attributes, image-only consumer filters, the two-PR delivery order, the CSP and media-origin checks, and the rename as its own commit.
- Already true in the code, no change needed: attachments are forced into their own block by `openBlockForImage`; the sideload route blocks private addresses on every hop and caps bytes and redirects; the indexer lowercases extensions; article pages do not use view transitions; the Instagram marker check runs on expanded content.
- Decided by Eden: Instagram's script is accepted as a trust decision; YouTube embeds stay on `youtube.com`.
