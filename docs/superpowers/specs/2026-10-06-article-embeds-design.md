# Article embeds: audio player and media embeds

Date: 2026-10-06, revised 2026-10-06 and 2026-10-07 after two council reviews
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
- The WordPress export (`wp-posts.xml`, 2026-05-06) contains 240 `[youtube ...]` brackets and 46 `[soundcloud ...]` brackets anywhere in the file. YouTube shapes: 181 `watch?v=`, 21 `watch?v=` plus `&w=560&h=315`, 18 bare `[youtube]`, 20 odd strings such as `[youtube: Marc Zapanta]`. SoundCloud: 43 carry `url=` and all 43 point at `api.soundcloud.com/tracks/...` with up to 13 attributes; 3 are bare `[soundcloud]`. Entities: 18 YouTube and all 43 SoundCloud strings use `&amp;`, none use `&#038;`, 3 YouTube strings use a raw `&`. Counting only the 246 occurrences inside `<content:encoded>` bodies: 241 sit alone on their line (162 inside `<p>`, 79 as bare text), 5 sit inline with prose, none share a line. The extraction script and its output are committed with the test fixtures so these numbers can be re-run. There is no `[audio ...]` shortcode; the six `[audio` hits are prose such as `[audio archives]`. `[Spotify]`, `[TikTok]` and `[instagram]` appear 12, 18 and 14 times as bare words in prose.
- Scalene has no UI framework and no islands. Interactivity is plain `<script>` tags (`astro.config.mjs:123-127`). Article pages use `ArticleLayout`, which does not include Astro's `ClientRouter`; only `BaseLayout` does (`src/layouts/BaseLayout.astro:2,110`). So inline scripts on article pages run on every full load and there is no view-transition re-run problem.
- One script-dependent embed already exists: AmuseLabs puzzles, whose script is injected only when the expanded body contains the marker div (`src/pages/[sections]/[article].astro:256, 308-313`). The `content` variable there is already expanded (line 108), so a marker check on it sees expander output.
- Scalene strips width and height out of every inline style in the body (`[article].astro:117-128`). Attributes such as `height="300"` on an iframe survive; the existing SoundCloud expander relies on that.
- Scalene has no DOM parser. `normalizeArticleHtml` walks top-level elements with its own `mapTopLevel` string walker (`src/utils/articleHtml.ts:18-60`), which hands each top-level `<p>` or `<div>` to a visitor and passes text between elements through untouched.
- Scalene rewrites media URLs on `cms.thetriangle.org` and `www.thetriangle.org` to its own `/proxy/wp-content/...` route (`[article].astro:110-113`). That route fetches the file, buffers it whole in memory, and forwards only `Content-Type`; it does not pass `Range` and cannot answer `206` (`src/pages/proxy/[...image].ts`). URLs on the `MEDIA_BASE_URL` host are not rewritten. The `.env.example` names `https://delta.thetriangle.org` as the example media origin.
- Both repos already strip any `[name key=value]` shortcode from excerpts through a generic pattern (`shortcodes.ts:165-181`, `http_models.go:780`), so the new shortcodes never reach excerpts, RSS or sitemap text without any change.
- `addBodySrcsets` touches only `<img>` tags (`[article].astro:209-212`).
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
- Entity contract. The stored body is HTML, so a `&` inside a URL is stored as `&amp;`. Scalene decodes the captured attribute value exactly once with `he.decode` before URL parsing, then escapes once on output. The editor reads `textContent`, which is already decoded. `embed-vectors.json` carries `&amp;`, `&#038;` and raw `&` cases.
- Case. `.mp3` is matched case-insensitively everywhere: the sniff fallback, the editor save check, and the Scalene audio check. A `.MP3` vector exists.
- A malformed shortcode stays visible as text. Nothing is dropped silently. This is the existing behavior at `shortcodes.ts:141-143`.
- No caption attribute. Writers put context in the paragraph above.

The audio example host is illustrative. The real host is whatever `MEDIA_BASE_URL` resolves to, and the path shape comes from `media.go:254-256`. Images already store absolute URLs built from the same variable (`media.go:148`), so audio shares their host-migration risk rather than adding a new one.

### Accepted inputs and canonical forms

Both repos ship the same test-vector file, `embed-vectors.json`, with accept and reject cases per provider. The frontend copy is the source; the Scalene copy is checked in beside it and CI in each repo runs its parser against its copy. A drift between them fails the Scalene build, which is the side readers see.

| Provider | Accepted paste shapes | Canonical stored URL |
|---|---|---|
| YouTube | `youtube.com/watch?v=ID`, `m.youtube.com/watch?v=ID`, `youtu.be/ID`, `youtube.com/shorts/ID`; ID matches `^[\w-]{11}$` | `https://www.youtube.com/watch?v=ID` |
| SoundCloud | host `soundcloud.com`, `www.soundcloud.com` or `api.soundcloud.com` | the share URL with the query reduced to `secret_token`, encoded per the grammar |
| Spotify | host `open.spotify.com`, path after prefix strip `^/(track\|episode\|show\|album\|playlist)/([A-Za-z0-9]{22})/?$` | `https://open.spotify.com/{type}/{id}` |
| TikTok | host `tiktok.com`, `www.tiktok.com` or `m.tiktok.com`, path `^/@([\w.-]+)/video/(\d+)/?$` | `https://www.tiktok.com/@{user}/video/{id}` |
| Instagram | host `instagram.com` or `www.instagram.com`, path `^/(?:[\w.]+/)?(p\|reel\|reels)/([A-Za-z0-9_-]+)/?$`; `reels` maps to `reel` | `https://www.instagram.com/{p\|reel}/{id}/` |

Parser rules, shared by both repos:

- Input goes through `new URL(input)`. Scheme must be `https` (or `http`, upgraded to `https`). Userinfo and a non-default port reject.
- Path regexes are anchored and run against `pathname`. YouTube takes the ID from `searchParams.get("v")` on `/watch`, and from the path on `youtu.be`, `/shorts/` and `/live/`. `t=` and `start=` are dropped.
- Spotify strips an optional `/intl-xx/` or `/intl-xx-yy/` prefix before the anchored match.
- Instagram accepts an optional leading username segment, `/{username}/p/{id}/`. `/tv/` is not accepted.
- SoundCloud keeps the share URL but drops every query parameter except `secret_token`, which private tracks need. `on.soundcloud.com` is a short-link host.
- `parseEmbedUrl` returns `{ ok: true, provider, url }` or `{ ok: false, reason: "short-link" | "invalid" }`. Known short-link hosts (`vm.tiktok.com`, `vt.tiktok.com`, `spotify.link`, `on.soundcloud.com`) produce a specific message: open the link in a browser and paste the full URL. Any other failure produces the generic message naming the five accepted hosts.

Vector drift. Both repos are public on GitHub. Scalene's CI step fetches the frontend's `embed-vectors.json` from `DrexelTriangle/triangle-cms` on `main` and diffs it against Scalene's copy; a mismatch fails the build. If the fetch itself fails, including the 404 before the CMS copy exists on `main`, the step is skipped with a warning, so an outage cannot block Scalene deploys and the check switches itself on the day the CMS PR merges. (Planning note, 2026-10-07: `main` rather than a pinned commit, because the Scalene PR ships first and there is no CMS commit to pin yet.)

## 5. Scalene rendering

Four new expanders in `src/utils/shortcodes.ts`, each shaped like the existing `soundcloudEmbed`: parse attributes, decode entities once, validate against the same rules as section 4, rebuild the output URL from the captured ID, return the block unchanged on any failure, size by class or attribute.

Where they run. The legacy `[youtube]` and `[soundcloud]` regexes in `expandShortcodes` are not touched; the 246 legacy occurrences keep rendering exactly as today. The four new expanders run in a separate pass, `expandBlockShortcodes`, built on the existing `mapTopLevel` walker. It visits each top-level `<p>` or `<div>` and fires only when the block's trimmed inner text is exactly one new shortcode. The whole block, open tag included, is replaced by the expander output, so a `<p>` never ends up wrapping a `<div>`. Because the match is the entire content of a top-level block, a shortcode inside an attribute value, a sentence, a `<code>` block, a `<script>` or a comment can never match. The editor only ever writes these shortcodes as the sole content of a `<div>`, so no bare-text support is needed. The pass runs inside `expandShortcodes` after the legacy regexes, so the article page's call at `[article].astro:108` and every existing caller stay as they are.

| Shortcode | Output |
|---|---|
| `[spotify url=]` | `.article-embed.article-embed-audio` wrapping an iframe at `https://open.spotify.com/embed/{type}/{id}` with `height="152"` for episode and track, `height="352"` otherwise, `allow="encrypted-media"`, `loading="lazy"`, `title="Spotify player"` |
| `[tiktok url=]` | `.article-embed.article-embed-video.article-embed-portrait` wrapping an iframe at `https://www.tiktok.com/player/v1/{id}` with `loading="lazy"`, `title="TikTok video"` |
| `[instagram url=]` | `.article-embed.article-embed-social` wrapping the official `<blockquote class="instagram-media" data-instgrm-permalink="{canonical}">` with a plain `<a href="{canonical}">View this post on Instagram</a>` inside as fallback |
| `[audio src=]` | `.article-embed.article-embed-audio` wrapping `<audio controls preload="metadata" src="{src}" aria-label="Audio player">` followed by `<a href="{src}">Open MP3</a>` |

Audio `src` validation in Scalene: parses as a URL; scheme `https`; no userinfo or port; origin equal to the origin of `MEDIA_BASE_URL`; `pathname` starts with `/wp-content/uploads/` and ends in `.mp3` case-insensitively; query and fragment are dropped and the normalized URL is what gets emitted. The editor applies every check except the origin comparison, because the admin app has no `MEDIA_BASE_URL` and the URL it stores always came from the server's own upload response or a library row; Scalene is the gate for origin. (Planning note, 2026-10-07.) Anything else stays as text. If `MEDIA_BASE_URL` is unset in Scalene's build, every `[audio]` stays as text; the build does not fail, because that variable is already optional for the rest of the site.

The link is labelled "Open MP3" rather than using `download`, because the `download` attribute is ignored across origins and the media host is a different origin from www.

Audio bypasses the Scalene media proxy by construction: the stored URL is on the `MEDIA_BASE_URL` origin, which `proxyWpContent` does not rewrite, so the browser fetches the file from the media origin directly with Range support. This holds only while the production `MEDIA_BASE_URL` is not `cms.thetriangle.org` or `www.thetriangle.org`; if it ever is, the proxy would buffer each 58 MB file in memory and seeking would not work. Confirming the production value is a pre-deploy check in section 9.

Every attribute value is passed through the existing `escapeAttribute`. Output URLs are built from the validated parts, so a writer's query string never reaches the page.

Where the expander runs. The expander is a regex over the whole body, so the new patterns match only when the shortcode starts at the beginning of the string, after `>`, or after a newline plus optional whitespace, and ends at the end of the string, before `<`, or before a newline. That excludes a shortcode inside an attribute value or inside a sentence. Before matching, `<pre>` and `<code>` ranges are masked so a code sample that shows a shortcode is left alone. Tests cover both.

Sources for the embed URL patterns: Spotify, developer.spotify.com/documentation/embeds/tutorials/creating-an-embed. TikTok, developers.tiktok.com/doc/embed-player. Instagram, developers.facebook.com/docs/instagram-platform/oembed. Instagram has no documented script-free form; the `/embed/` iframe some sites use is undocumented and is not used here.

TikTok's player query. The embed-player docs offer `description=1` and `music_info=1`. Whether the player then exceeds a 9:16 box is checked in a browser during the Scalene PR; if it clips, the parameters are dropped rather than the aspect ratio changed.

Instagram script: `hasInstagramEmbed = content.includes('<blockquote class="instagram-media"')` in `[article].astro`, the exact opening tag the expander emits, computed on the expanded `content` exactly like `hasPuzzleScript`, and a conditional `<script is:inline async src="https://www.instagram.com/embed.js">` next to the AmuseLabs script at lines 308-313. No `integrity` hash: Instagram rotates embed.js without versioned URLs, so a pinned hash would break the embed on their next deploy. If the script does not load, the reader sees the plain link.

CSS in `global.css`, next to lines 230-251:

- `.article-embed iframe { width: 100%; border: 0 }` as a base rule, so a height attribute sets the height and the box fills its column.
- `.article-embed-portrait { position: relative; aspect-ratio: 9 / 16; max-width: 325px; margin-inline: auto }` with `.article-embed-portrait iframe { position: absolute; inset: 0; width: 100%; height: 100% }`, mirroring the existing 16:9 rule at `global.css:236-251`. Without the absolute fill an iframe sits at its 150px default height inside the box.
- `.article-embed-social`: no fixed aspect ratio. Instagram sizes its own blockquote.

Pipeline order, so the splitter argument holds: `expandShortcodes` runs first (`[article].astro:108`), then `normalizeArticleHtml`. By the time the paragraph splitter sees the body, every expanded shortcode is a top-level `<div>` with no `<p>` around it, and `div` is in the splitter's skip list (`articleHtml.ts:39`). Tests feed the output through `normalizeArticleHtml` and parse the result with the Node DOM used by Vitest's jsdom environment, checking there is no `<p>` wrapping a `<div>` and no empty `<p>` siblings. One test places a bare legacy shortcode between two paragraphs.

SoundCloud share URLs. The existing expander was written against the 43 `api.soundcloud.com/tracks/...` URLs. The editor will now write `soundcloud.com/{user}/{track}` URLs. SoundCloud's player accepts both forms in its `url` parameter per the existing expander's own comment, but this is confirmed in a browser during PR 1 before the vectors are frozen.

Not done here: YouTube and SoundCloud rendering (already works), oEmbed calls, short links.

## 6. CMS: accept MP3 uploads

1. Allowlist. Rename `allowedImageTypes` to `allowedUploadTypes` in `media.go` and add `"audio/mpeg": ".mp3"`. The sideload route uses the same map and already has the protections listed in section 2, so remote MP3 URLs also work. The sideload filename comes from the URL path with the query removed. The sideload timeout stays 30 s for images; when the response `Content-Type` or the URL extension says MP3, the per-request timeout is 5 minutes, since 90 MiB in 30 s needs 3 MB/s from the remote.
2. Sniff fallback. Go's `http.DetectContentType` returns `audio/mpeg` only when the file starts with an ID3 tag. Checked on go1.26.4 with synthetic headers: ID3 gives `audio/mpeg`; a bare MPEG frame and an M4A both give `application/octet-stream`. After the sniff, one extra branch: if the result is `application/octet-stream` and the client filename ends in `.mp3`, validate a full 4-byte MPEG audio frame header: 11 sync bits set, version bits not `01`, layer bits not `00`, bitrate index not `1111`, sample-rate index not `11`. If it passes, treat the file as `audio/mpeg`. The 11-bit sync alone would also admit AAC ADTS frames. Tests use real MP3 fixtures of a few kilobytes as well as synthetic headers. M4A and WAV stay rejected.
3. Indexer. Add `".mp3": "audio/mpeg"` to `mediaMimeByExt` (`database/media.go:38-45`). For `.mp3` only, the indexer opens the file and runs the same header check as upload, skipping files that fail; the comment at `media.go:35-37` explains why images are not sniffed on reindex, and the exception is bounded because MP3s are few. A reindex then imports every valid MP3 already under `wp-content/uploads`. Before merging, count them on the server with `find $MEDIA_ROOT/wp-content/uploads -iname '*.mp3' | wc -l` and confirm the import is wanted.
4. Size limit. Keep 90 MiB. A one-hour podcast at 128 kbps is about 58 MB.
5. Dimensions. `imageDimensions` returns 0, 0 for a file it cannot decode (`media.go:668-680`). Audio rows store width and height as 0. No schema change; `media` already has `mime_type` and `size_bytes`.
6. Renditions. `MediaNeedingRenditions` selects only `image/jpeg`, `image/png` and `image/webp` (`database/media_renditions.go:64-79`). Audio rows never reach the libvips sidecar.
7. Image-only consumers. Every place that lists media and expects an image gets a filter on `mime_type LIKE 'image/%'`: the featured-image picker, gallery inserts, image replacement, and any og:image lookup. The implementation plan enumerates them from `rg -n 'ListMedia|/v1/media' frontend/src server/internal`.
8. Media library. Extend the picker's `accept` list at `frontend/src/components/MediaPicker.tsx:239` with `audio/mpeg,.mp3`. A library item whose `mime_type` starts with `audio/` renders a generic audio tile instead of an `<img>`.

Server-side checks before the CMS PR deploys, recorded in the PR. None of these edits nginx, Cloudflare or the deploy scripts; if a check fails, the result goes to Sachin as a question, since those live outside the repos.

- A real 60 MB MP3 pushed through Cloudflare, nginx and Go. nginx `client_max_body_size` and Go timeouts must clear it.
- `curl -s -r 0-1 -D - -o /dev/null <media url>` through Cloudflare returns `206`, a `Content-Range` header, `Content-Type: audio/mpeg` and two bytes. A HEAD request does not prove ranged GETs work.
- The media origin sends `X-Content-Type-Options: nosniff` and the extension-mapped `Content-Type`. This is the real polyglot defense: any file starting with `ID3` passes the header check, so the browser must be told never to guess. If the header is absent, audio does not ship until it is added by whoever owns nginx.
- The media origin is not the admin origin, so a served file can never run as the admin app.
- The production `MEDIA_BASE_URL` for Scalene's build is on a host other than `cms.thetriangle.org` and `www.thetriangle.org` (see section 5, proxy bypass).

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

- `trix-file-accept` gains a check. Accepted: an image type the server allows, `audio/mpeg`, `audio/mp3`, `audio/mpeg3`, or an empty type when the filename ends in `.mp3` case-insensitively. Browsers vary in how they report MP3s, so the type string alone never decides. A WAV or M4A is refused here rather than after a 90 MB upload. Anything else: prevent default and show a notice.
- After `POST /v1/media` returns, the handler sets `url` and sets `contentType` from the response's `content_type`, not from the browser's `File.type`. The server's sniffed type is the one the save branch keys on.
- Trix renders a non-previewable attachment as a filename chip, which is the editor view for audio.
- Inserting an audio row from the picker builds an attachment with `contentType` from the row's `mime_type`.

### Pending uploads

Today a save during an image upload leaves a `blob:` figure in the Trix markup, which the serializer skips. The same would happen to a 58 MB audio upload, and worse: a non-previewable attachment has no `url` at all until the upload finishes, so a `blob:` check would not see it. Fix for both: the editor exposes `hasPendingUploads()`, which walks the document's attachments and returns true if any `attachment.isPending()` (Trix 2.1.19: a file is set and neither URL nor href is, `trix.esm.js:6771-6773`). `editArticleView` calls it synchronously at the top of every save and autosave handler, including the keyboard shortcut path, and refuses with "Waiting for upload to finish" while it returns true; the button is also disabled as a visual cue. The XHR upload gets a 10-minute timeout; on timeout `failAttachment` removes the chip and reports it, so a hung upload cannot block saving forever. This changes image behavior too, and the PR says so.

### Serialization

`frontend/src/components/trixImageHtml.ts` becomes `trixArticleHtml.ts` because it no longer only handles images. The rename is its own commit with no behavior change, so the review diff shows only the new branches.

Current behavior, pinned by a test before anything changes: at line 236, a figure whose `contentType` is not an image is left exactly as Trix wrote it, `data-trix-attachment` and all. It is not deleted. Readers would see Trix's figure markup, which the public site does not style.

- Save, `trixHtmlToArticle`. The line 236 skip becomes a branch:
  - `contentType === "application/x-triangle-embed"`: re-run `parseEmbedUrl(embedUrl)`. If `embedOriginal` is set, it is reparsed too and must match the strict shape `^<(p|div)(\s[^>]*)?>\s*\[...\]\s*</\1>$` or a bare shortcode, with its shortcode resolving to the same provider and canonical URL; only then is it emitted verbatim, as the original block string, which the shape check has already constrained to one wrapper element around one shortcode. Otherwise the canonical shortcode is emitted as the `textContent` of a new `<div>`. If `parseEmbedUrl` fails, the save is refused with an error naming the block ("Embed 3 has an invalid URL"), the same way a pending upload refuses it. Nothing invalid is ever written to the article.
  - `contentType` starting `audio/`: validate `url` with the shared audio rules. If it fails, including `blob:`, `data:` or an unset URL, the save is refused with an error naming the block. Otherwise emit `[audio src="URL" /]` as the `textContent` of its own `<div>`.
  - Anything else passes through unchanged, as today.
- Load, `articleHtmlToTrix`. Two passes before the figure pass.
  - Every existing figure whose `contentType` is the embed type or starts with `audio/` is rebuilt: its stored `content` is discarded and the card is regenerated from revalidated attributes. If the attributes do not validate, the figure becomes an inert error card ("Invalid embed, remove this block") with the same content type and no URL, so the save branch above refuses it until the writer deletes it. Trix therefore never renders stored HTML from the database.
  - For each top-level `<p>` or `<div>` whose trimmed inner text is exactly one shortcode that `shortcodeToEmbed` recognizes, or an `[audio src=]` that validates, the block is replaced with a figure built by `createElement` and `setAttribute("data-trix-attachment", JSON.stringify(attrs))`. The attrs carry the canonical `embedUrl` and, for legacy blocks, `embedOriginal` holding the entire original block string. The 5 legacy shortcodes inline with prose, the 18 bare `[youtube]` tags, the 20 odd strings and the 79 bare-text legacy shortcodes fail the block match and stay as text in the editor; Scalene still renders the bare-text ones through the legacy regex. A `<div>` holding shortcode-shaped prose does load as a card, which is acceptable because Scalene would render it the same way; a test pins this.
- Paste and drop. `trix-attachment-add` already fires for pasted attachments. It gains the same rule as load: any attachment with the embed content type or an `audio/` type has its `content` replaced by a card rebuilt from revalidated attributes, or is removed with a notice if they do not validate. HTML pasted from another tab therefore cannot carry hostile `content` into the editor.

### Error handling

- Parse failure: one-line notice in the prompt, nothing inserted.
- Upload failure: the existing `failAttachment` path removes the chip and shows the server message.
- An embed figure with missing or invalid attributes becomes a visible error card on load and blocks the save until removed. Nothing is deleted silently and nothing invalid is published.

Not done here: live player in the editor, thumbnails or titles, captions on embeds, changes to drag-reorder (the existing overlay handles any figure).

## 8. Tests

### triangle-cms server (Go, stdlib testing)

- `media_test.go`: an ID3-tagged MP3 is stored as `audio/mpeg` with `.mp3`; a bare-frame MP3 named `.mp3` is accepted; the same bytes named `.bin` are rejected; an AAC ADTS header named `.mp3` is rejected; an M4A is rejected; a PNG still works. Fixtures: two real MP3 files of a few kilobytes plus synthetic headers.
- `media_renditions_test.go`: an `audio/mpeg` row is never returned by `MediaNeedingRenditions`.

### triangle-cms frontend (Vitest)

- `embedUrl.test.ts`: every accept and reject vector in `embed-vectors.json`; hostile inputs (`"`, `]`, `javascript:`, `?x="><img onerror=...>`) rejected or encoded; `shortcodeToEmbed(embedToShortcode(x))` equals `x` for every provider.
- `trixArticleHtml.test.ts`, new, since the module has no tests today:
  - corpus round trip: every distinct legacy block from `wp-posts.xml` whose shortcode parses, in each of its wrappers and with its original wrapper attributes, loads and saves to a block whose decoded, trimmed shortcode equals the original's. Byte identity is not the bar, because DOM serialization normalizes entities; the original block string is preserved through `embedOriginal` where the strict shape allows. The fixture, the extraction script and its counts are committed.
  - canonical round trip per provider and for audio.
  - a figure with missing or invalid attributes passes through unchanged.
  - an `audio/mpeg` attachment on a `blob:` URL passes through unchanged.
  - a stored figure carrying hostile `content` loads with the card rebuilt and the stored HTML absent; one with invalid attributes loads as an error card and the save is refused.
  - a crafted `embedOriginal` that does not match the strict shape, or whose shortcode resolves to a different URL, is not emitted; the canonical shortcode is written instead.
  - `.MP3` uppercase round-trips.
  - the image cases from `editArticleView.test.tsx` that touch serialization still pass.
- `editArticleView.test.tsx`, existing jsdom plus Trix harness, new cases: insert an embed with the caret mid-paragraph and check it lands in its own block; insert next to an image; undo and redo keep the attributes; copy and paste of an embed figure keeps them and a pasted figure with hostile `content` is rebuilt; picker insertion of an audio row; save and autosave both refuse while an attachment `isPending()`; a timed-out upload is removed; the `permittedAttributes` push happens once.

### Scalene

The repo has no test runner. Add Vitest (Node environment), a `test` script, and a `vitest run` step in `.github/workflows/ci.yml` beside `astro check`. One file, `src/utils/shortcodes.test.ts`: every vector in the repo's copy of `embed-vectors.json`; each expander on malformed input returning the block unchanged; `[Spotify]` with no `url=` left as text; a shortcode inside `<code>`, inside an attribute value, inside `<script>` and inside a sentence left as text; a shortcode as the sole content of a `<p>` replaces the whole `<p>` with no empty `<p>` siblings after `normalizeArticleHtml`, checked on the parsed DOM; `&amp;` and `&#038;` in a URL decode once and never double-escape; a bare legacy shortcode between two paragraphs still renders; every emitted attribute value is escaped; `[audio]` stays as text when `MEDIA_BASE_URL` is unset. This is the first test file in Scalene; say so in the PR.

### Manual

In the admin editor, insert each of the five embeds plus one MP3, save, reload, confirm the cards return. Open the article on the local Scalene dev server and confirm each player renders, including the TikTok box at phone width. Repeat on dev.thetriangle.org after merge.

## 9. Delivery

Two pull requests, Scalene first. The server allowlist ships in the same PR as the editor, so there is never a window where MP3s can be uploaded but not placed.

1. Scalene: four expanders, Instagram script, CSS, `embed-vectors.json`, Vitest setup and tests. Inert until something writes the shortcodes. Must be live in production before PR 2 deploys, or readers would see raw shortcode text.
2. triangle-cms: rename commit, then server allowlist and sniff fallback and indexer with Go tests, then `embedUrl.ts`, serialization branches, toolbar button, pending-upload guard, picker changes and Vitest tests. Deploys only after PR 1 is confirmed on www.thetriangle.org.

Rollback rule. Once PR 2 is live, the CMS writes the new shortcodes into articles. Scalene's expanders are therefore never reverted while PR 2 is deployed; a Scalene rollback would show raw shortcode text on those articles. If PR 1 needs reverting after PR 2 ships, PR 2 is reverted first.

Before PR 1 deploys, audit the effective production Content-Security-Policy across Scalene middleware (none today), nginx and Cloudflare. Required entries, if a policy exists: `frame-src` for `www.youtube.com`, `w.soundcloud.com`, `open.spotify.com`, `www.tiktok.com` and `www.instagram.com` (embed.js creates an iframe); `script-src` for `www.instagram.com` and the existing `puzzleme.amuselabs.com`; `media-src` for the media origin. Every provider is then tested under the effective policy on dev.thetriangle.org. If no policy exists, note that in the PR and leave adding one out of scope.

Image-only consumers, enumerated. CMS frontend: the featured-image picker in `editArticleView.tsx`, the gallery insert and image replacement flows in `MediaPicker.tsx`, and the library page; each filters on `mime_type` starting `image/` where an image is required. CMS server: the gallery endpoints and `content_image_variants` lookup already key on image rows through the renditions table. Scalene: `addBodySrcsets` matches `<img>` only, og:image reads `featured_image`, and excerpts for RSS and sitemaps strip `[name key=value]` shortcodes already. No Scalene change is needed; the plan re-checks each with `rg` before closing.

Privacy. The five providers set their own cookies on readers when an article embeds them, with no consent gate, which matches how YouTube, SoundCloud, AdSense and Matomo already behave on the site. Eden records this as accepted for this pass. Click-to-load placeholders for TikTok and Instagram are a possible follow-up.

Third parties that load on an article page after this change, for the record: YouTube, SoundCloud, Spotify, TikTok and Instagram iframes or scripts, each only when an article embeds them, plus the existing AdSense, Matomo and AmuseLabs. Each sets its own cookies on readers.

## 10. Out of scope

Video upload. oEmbed, thumbnails and titles. Short links. Captions on embeds. M4A and WAV. A dedicated audio library view. `youtube-nocookie.com`. A Content-Security-Policy where none exists today. Click-to-load consent placeholders. Changes to the Scalene media proxy, nginx, Cloudflare or deploy scripts. Quizzes, timelines and charts, which are the next two sub-projects.

## 11. Council reviews

### Second review, 2026-10-07, four models

Constraint applied to every finding: no change to how either repo already builds, deploys or serves. Disposition:

- Applied inside the repos: the new expanders run as a block pass on the existing `mapTopLevel` walker instead of string positions, replacing the whole `<p>`; entities decode once; `embedOriginal` is reparsed and shape-checked before verbatim output; invalid figures become error cards and block the save; paste and drop rebuild cards; `isPending()` drives the save guard with a synchronous check and an upload timeout; TikTok iframe fills its box; SoundCloud short host rejected and query reduced to `secret_token`; vector drift checked in Scalene CI against the pinned public repo, skipped on fetch failure; `.mp3` case-insensitive; audio URL validated on full origin and path prefix; YouTube ID from the query, Spotify prefix strip, Instagram username segment, discriminated parser result; corpus counts reconciled and the script committed; `trix-file-accept` narrowed to MP3 types; audio sideload timeout; reindex sniffs `.mp3`; ranged GET test; "Open MP3" label; Instagram marker matched on the exact emitted tag; CSP entries for Instagram frames and `media-src`; consumers enumerated; rollback rule; privacy recorded.
- Deliberately not done, to leave infrastructure alone: no new HTML parser dependency (the existing walker suffices); no Scalene build failure when `MEDIA_BASE_URL` is unset; no cross-repo environment check; no nginx, Cloudflare, proxy or deploy-script edits, with every server-side requirement turned into a pre-deploy check that goes to Sachin if it fails; no `Content-Disposition` download variant.
- Found while verifying: Scalene's media proxy buffers whole files and has no Range support, so audio must bypass it, which it does while the production media host is not a legacy host. Recorded as a pre-deploy check.

### First review, 2026-10-06, three models

Findings and their disposition:

- Applied: anchored regexes on `pathname`, URLs rebuilt from IDs, an attribute grammar, `setAttribute` plus `JSON.stringify` for the Trix JSON, revalidation on save, https and media-host-only audio, preview cards rebuilt from attributes, lossless legacy round trip via `embedOriginal`, shared test vectors, per-provider templates, `contentType` from the server response, a pending-upload save guard, hidden caption field, a 4-byte MPEG header check, expander position rules and `<pre>`/`<code>` masking, iframe base CSS, accessibility attributes, image-only consumer filters, the two-PR delivery order, the CSP and media-origin checks, and the rename as its own commit.
- Already true in the code, no change needed: attachments are forced into their own block by `openBlockForImage`; the sideload route blocks private addresses on every hop and caps bytes and redirects; the indexer lowercases extensions; article pages do not use view transitions; the Instagram marker check runs on expanded content.
- Decided by Eden: Instagram's script is accepted as a trust decision; YouTube embeds stay on `youtube.com`.
