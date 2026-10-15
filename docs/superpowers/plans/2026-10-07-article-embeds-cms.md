# Article embeds, CMS side: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a writer insert a YouTube, SoundCloud, Spotify, TikTok or Instagram link, or an uploaded MP3, from the Trix editor, stored as shortcodes the public site already renders (Scalene PR first).

**Architecture:** The Go server accepts `audio/mpeg` uploads through the existing sniffing allowlist with a 4-byte MPEG frame-header fallback. The React editor gains one new attachment kind (a Trix content attachment carrying `embedProvider` and `embedUrl`) and one new toolbar button; both the load and save sides of `trixArticleHtml.ts` get a branch that converts between Trix figures and shortcodes. URL rules live in `frontend/src/lib/embedUrl.ts`, a byte-identical copy of Scalene's module, driven by a shared `embed-vectors.json`.

**Tech Stack:** Go 1.22+ stdlib (`net/http`, `testing`), React 19, TypeScript strict, Trix 2.1.19, Vitest 3 with jsdom and Testing Library.

**Spec:** `docs/superpowers/specs/2026-10-06-article-embeds-design.md` (sections 2, 4, 6, 7, 8, 9). Read it before starting. The Scalene plan is at `/Users/eden/Documents/code/triangle/Scalene/docs/superpowers/plans/2026-10-07-article-embeds-scalene.md`; its Task 2 defines the shared module this plan copies in Task 6.

## Global constraints

- Branch: `feature/article-embeds-spec` already exists from `main` at `50dcf74`; do all work there (rename it to `feature/article-embeds` with `git branch -m` first). Commit after every task. Never push or open a PR; Eden does that per his standing rule.
- The Scalene PR must be live on www.thetriangle.org before this branch deploys. This branch may merge earlier, but deploy order is Scalene first.
- No edits to nginx, Cloudflare, deploy scripts, `compose.*.yml`, `.env` handling or the Scalene media proxy. Every server-side requirement in section 6 of the spec is a pre-deploy check (Task 13), never a code change here.
- `frontend/src/lib/embedUrl.ts` and `frontend/src/lib/embed-vectors.json` must stay byte-identical to Scalene's `src/utils/embedUrl.ts` and `src/utils/embed-vectors.json`.
- All HTML built in the editor goes through `createElement`, `setAttribute`, `textContent`. No string-concatenated markup from user data. Stored `content` on an embed figure is never rendered; the card is rebuilt from validated attributes.
- `.mp3` matches case-insensitively everywhere.
- Go: run `cd server && go test ./... && go vet ./...` after each server task. Swagger annotations do not change (no new routes), so `swag init` is not needed; CI's drift check still runs, so if any annotation line is touched, run `swag init --parseDependency --parseInternal` in `server/` and commit `server/docs/`.
- Frontend: run `cd frontend && npm test -- <file>` per task and `npm run lint && npm run build` before the final commit.
- Comments and PR text: sentence case, no em dashes, straight quotes.

## Review focus

Inputs the spec implies but no earlier draft tested. Each has a test in the task that owns it.

1. A writer pastes a YouTube URL while the caret is in the middle of a paragraph; the card must land in its own block with the paragraph split around it (Task 10).
2. A writer drops a `.wav` file; it must be refused before any upload starts (Task 10).
3. An article loaded from the database carries a figure whose `data-trix-attachment` JSON has `contentType: "application/x-triangle-embed"` and a hostile `content`; the editor must show a card built from the attributes, never the stored HTML (Task 8).
4. A writer hits Save while a 50 MB MP3 is still uploading; the save must be refused with a visible message and the content must not be sent (Task 11).
5. A legacy article with `<p>[youtube https://www.youtube.com/watch?v=ID&amp;w=560&amp;h=315]</p>` is opened and saved without edits; the stored block must come back unchanged (Task 8).

---

### Task 1: Rename the serialization module

**Files:**
- Rename: `frontend/src/components/trixImageHtml.ts` to `frontend/src/components/trixArticleHtml.ts`
- Modify: `frontend/src/components/TrixEditor.tsx:8-12` (import path)

**Interfaces:**
- Produces: the same exports (`articleHtmlToTrix`, `trixHtmlToArticle`, `contentTypeForUrl`, `ALIGNMENTS`, `TRIX_ALIGN_CLASS`) from the new path. No behavior change.

- [ ] **Step 1: Rename the branch and the file**

```bash
git branch -m feature/article-embeds-spec feature/article-embeds
cd frontend
git mv src/components/trixImageHtml.ts src/components/trixArticleHtml.ts
```

- [ ] **Step 2: Update the import in TrixEditor.tsx**

Change lines 8-12 from `} from "./trixImageHtml";` to `} from "./trixArticleHtml";`.

- [ ] **Step 3: Update the header comment's self-reference**

In `trixArticleHtml.ts`, the first comment block does not name the file, so nothing changes there. Search for the old name anywhere else:

Run: `rg -n trixImageHtml src`
Expected: no matches. If `editArticleView.test.tsx` mentions it, update that string too.

- [ ] **Step 4: Run the frontend tests and build**

Run: `npm test && npm run build`
Expected: all existing tests pass, build succeeds.

- [ ] **Step 5: Commit**

```bash
git add -A src/components/trixImageHtml.ts src/components/trixArticleHtml.ts src/components/TrixEditor.tsx
git commit -m "refactor: rename trixImageHtml to trixArticleHtml"
```

---

### Task 2: Pin the module's current behavior

**Files:**
- Create: `frontend/src/components/trixArticleHtml.test.ts`

**Interfaces:**
- Consumes: `articleHtmlToTrix(html: string): string`, `trixHtmlToArticle(html: string): string`.

- [ ] **Step 1: Write the tests**

```ts
import { describe, expect, it } from "vitest"

import { articleHtmlToTrix, trixHtmlToArticle } from "./trixArticleHtml"

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
    expect(out).toContain('"caption":"Cap"')
    expect(out.startsWith("<div><figure")).toBe(true)
  })

  it("returns plain prose untouched", () => {
    expect(articleHtmlToTrix("<p>Hello</p>")).toBe("<p>Hello</p>")
  })
})
```

- [ ] **Step 2: Run**

Run: `npm test -- trixArticleHtml`
Expected: 5 passed. If the first assertion's exact string differs by attribute order, copy the actual output into the expectation after confirming it is the same markup; the point is to pin it.

- [ ] **Step 3: Commit**

```bash
git add src/components/trixArticleHtml.test.ts
git commit -m "test: pin trixArticleHtml behavior before adding embeds"
```

---

### Task 3: Server accepts MP3 uploads

**Files:**
- Create: `server/internal/mediatype/mediatype.go`
- Create: `server/internal/mediatype/mediatype_test.go`
- Modify: `server/internal/handlers/media.go:80-88` (allowlist) and `:241-246` (sniff)
- Modify: `server/internal/handlers/media_test.go` (new cases)
- Create: `server/internal/mediatype/testdata/id3.mp3`, `server/internal/mediatype/testdata/bare.mp3`

**Interfaces:**
- Produces: package `mediatype` with
  - `func DetectUpload(head []byte, filename string) (contentType, ext string, ok bool)`
  - `func IsMPEGAudioFrameHeader(b []byte) bool`
  - `var AllowedUploadTypes = map[string]string{...}` (jpeg, png, gif, webp, mpeg audio)
- Task 5 imports `IsMPEGAudioFrameHeader` from the `database` package, which is why this lives in its own package rather than `handlers`.

- [ ] **Step 1: Generate two small real MP3 fixtures**

`ffmpeg` and `lame` are installed at `/usr/local/bin`. From `server/internal/mediatype/`:

```bash
mkdir -p testdata
# 0.3 s of silence, lowest bitrate, with an ID3v2 tag (ffmpeg writes one by default).
ffmpeg -y -f lavfi -i anullsrc=r=22050:cl=mono -t 0.3 -b:a 8k -id3v2_version 3 -metadata title=fixture testdata/id3.mp3
# Same audio with no tag and no Xing header, so the file starts on a frame sync.
ffmpeg -y -f lavfi -i anullsrc=r=22050:cl=mono -t 0.3 -b:a 8k -write_xing 0 -map_metadata -1 -fflags +bitexact -f mp3 testdata/bare.mp3
xxd -l 4 testdata/id3.mp3   # expect 4944 33..  ("ID3")
xxd -l 4 testdata/bare.mp3  # expect ff f? or ff e? (frame sync)
ls -l testdata              # each well under 10 KB
```

If `bare.mp3` still starts with `ID3`, strip the tag: `python3 -c "d=open('testdata/bare.mp3','rb').read(); i=d.find(b'\xff\xfb'); open('testdata/bare.mp3','wb').write(d[i:])"`.

- [ ] **Step 2: Write the failing tests**

Create `server/internal/mediatype/mediatype_test.go`:

```go
package mediatype

import (
	"os"
	"testing"
)

func readHead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if len(b) > 512 {
		b = b[:512]
	}
	return b
}

func TestIsMPEGAudioFrameHeader(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want bool
	}{
		{"mpeg1 layer3 128kbps 44.1k", []byte{0xFF, 0xFB, 0x90, 0x64}, true},
		{"mpeg2 layer3", []byte{0xFF, 0xF3, 0x90, 0x64}, true},
		{"aac adts (layer bits 00)", []byte{0xFF, 0xF1, 0x50, 0x80}, false},
		{"reserved version", []byte{0xFF, 0xEB, 0x90, 0x64}, false},
		{"bad bitrate index", []byte{0xFF, 0xFB, 0xF0, 0x64}, false},
		{"bad sample rate index", []byte{0xFF, 0xFB, 0x9C, 0x64}, false},
		{"no sync", []byte{0x00, 0xFB, 0x90, 0x64}, false},
		{"too short", []byte{0xFF, 0xFB}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		if got := IsMPEGAudioFrameHeader(c.head); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestDetectUpload(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	cases := []struct {
		name     string
		head     []byte
		filename string
		wantType string
		wantExt  string
		wantOK   bool
	}{
		{"png", png, "pic.png", "image/png", ".png", true},
		{"id3 mp3", readHead(t, "id3.mp3"), "ep.mp3", "audio/mpeg", ".mp3", true},
		{"bare mp3 named .mp3", readHead(t, "bare.mp3"), "ep.mp3", "audio/mpeg", ".mp3", true},
		{"bare mp3 named .MP3", readHead(t, "bare.mp3"), "EP.MP3", "audio/mpeg", ".mp3", true},
		{"bare mp3 named .bin", readHead(t, "bare.mp3"), "ep.bin", "application/octet-stream", "", false},
		{"aac named .mp3", []byte{0xFF, 0xF1, 0x50, 0x80, 0, 0, 0, 0}, "ep.mp3", "application/octet-stream", "", false},
		{"m4a", append([]byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'M', '4', 'A', ' '}, make([]byte, 20)...), "ep.m4a", "application/octet-stream", "", false},
		{"text named .mp3", []byte("hello, this is text"), "ep.mp3", "text/plain; charset=utf-8", "", false},
	}
	for _, c := range cases {
		gotType, gotExt, gotOK := DetectUpload(c.head, c.filename)
		if gotType != c.wantType || gotExt != c.wantExt || gotOK != c.wantOK {
			t.Errorf("%s: got (%q, %q, %v) want (%q, %q, %v)", c.name, gotType, gotExt, gotOK, c.wantType, c.wantExt, c.wantOK)
		}
	}
}
```

- [ ] **Step 3: Run to see them fail**

Run: `cd server && go test ./internal/mediatype/`
Expected: build failure, package has no non-test files.

- [ ] **Step 4: Write the package**

Create `server/internal/mediatype/mediatype.go`:

```go
// Package mediatype decides what an uploaded file is from its leading bytes.
// Shared by the upload handler and the media indexer so both admit exactly the
// same set of files.
package mediatype

import (
	"net/http"
	"path/filepath"
	"strings"
)

// AllowedUploadTypes maps a sniffed content type to its canonical extension.
// Only these types are accepted; the extension comes from the bytes, never from
// the client filename.
var AllowedUploadTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"audio/mpeg": ".mp3",
}

// DetectUpload sniffs head (the first 512 bytes) and reports the content type,
// its extension, and whether the file is accepted.
//
// Go's sniffer recognises an MP3 only by its ID3 tag. An MP3 with no tag starts
// straight on an MPEG frame and sniffs as application/octet-stream, so when the
// client called the file .mp3 the frame header is checked by hand. Any file
// starting with "ID3" passes the sniffer regardless of what follows; the media
// origin must therefore serve X-Content-Type-Options: nosniff (see the spec).
func DetectUpload(head []byte, filename string) (contentType, ext string, ok bool) {
	contentType = http.DetectContentType(head)
	if ext, ok = AllowedUploadTypes[contentType]; ok {
		return contentType, ext, true
	}
	if contentType == "application/octet-stream" &&
		strings.EqualFold(filepath.Ext(filename), ".mp3") &&
		IsMPEGAudioFrameHeader(head) {
		return "audio/mpeg", ".mp3", true
	}
	return contentType, "", false
}

// IsMPEGAudioFrameHeader reports whether b starts with a plausible MPEG audio
// frame header: 11 sync bits, a non-reserved version, a non-reserved layer, a
// valid bitrate index and a valid sample-rate index. The 11 sync bits alone
// would also match AAC ADTS frames, whose layer bits are 00.
func IsMPEGAudioFrameHeader(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	if b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return false
	}
	version := (b[1] >> 3) & 0x03
	if version == 0x01 {
		return false // reserved
	}
	layer := (b[1] >> 1) & 0x03
	if layer == 0x00 {
		return false // reserved; also what AAC ADTS carries
	}
	bitrate := b[2] >> 4
	if bitrate == 0x0F {
		return false
	}
	sampleRate := (b[2] >> 2) & 0x03
	if sampleRate == 0x03 {
		return false
	}
	return true
}
```

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/mediatype/ -v`
Expected: both tests pass. If "bare mp3" fails with `text/plain` or similar, the fixture still has a tag or Xing frame; redo Step 1's strip command.

- [ ] **Step 6: Wire the handler to the package**

In `server/internal/handlers/media.go`:

Delete the `allowedImageTypes` block at lines 80-88 and its comment.

Add `"server/internal/mediatype"` to the import list.

Replace lines 241-246:

```go
	contentType := http.DetectContentType(head[:n])
	ext, ok := allowedImageTypes[contentType]
	if !ok {
		return models.MediaUploadResponse{}, fmt.Errorf("%w: %s", errUnsupportedType, contentType)
	}
```

with:

```go
	contentType, ext, ok := mediatype.DetectUpload(head[:n], filename)
	if !ok {
		return models.MediaUploadResponse{}, fmt.Errorf("%w: %s", errUnsupportedType, contentType)
	}
```

Run: `rg -n allowedImageTypes internal`
Expected: no matches.

- [ ] **Step 7: Add handler-level tests**

Append to `server/internal/handlers/media_test.go`:

```go
func TestPostMedia_RejectsBareMP3BytesWithWrongExtension(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	rec := httptest.NewRecorder()
	// A real frame sync, but the client called it .bin: not an MP3 we accept.
	head := append([]byte{0xFF, 0xFB, 0x90, 0x64}, bytes.Repeat([]byte{0}, 600)...)
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, "audio.bin", head))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body = %s", rec.Code, rec.Body.String())
	}
}

func TestPostMedia_RejectsAACNamedMP3(t *testing.T) {
	t.Setenv("MEDIA_ROOT", t.TempDir())
	rec := httptest.NewRecorder()
	head := append([]byte{0xFF, 0xF1, 0x50, 0x80}, bytes.Repeat([]byte{0}, 600)...)
	PostMedia(nil).ServeHTTP(rec, uploadRequest(t, "audio.mp3", head))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body = %s", rec.Code, rec.Body.String())
	}
}
```

The accept path writes a database row, so it is covered in the integration test file. Append to `server/internal/handlers/media_integration_test.go`, inside the file's existing `MediaHTTP` test family (find `func TestMediaHTTP` and add a sibling):

```go
func TestMediaHTTP_AcceptsMP3Upload(t *testing.T) {
	conn := mediaHTTPTestDB(t)
	t.Setenv("MEDIA_ROOT", t.TempDir())
	t.Setenv("MEDIA_BASE_URL", "https://media.test")

	mp3, err := os.ReadFile(filepath.Join("..", "mediatype", "testdata", "bare.mp3"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rec := httptest.NewRecorder()
	PostMedia(conn).ServeHTTP(rec, uploadRequest(t, "Episode One.MP3", mp3))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var stored models.MediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stored.ContentType != "audio/mpeg" {
		t.Errorf("content_type = %q, want audio/mpeg", stored.ContentType)
	}
	if !strings.HasSuffix(stored.Path, ".mp3") {
		t.Errorf("path = %q, want .mp3 suffix", stored.Path)
	}
	if stored.Width != 0 || stored.Height != 0 {
		t.Errorf("dimensions = %dx%d, want 0x0 for audio", stored.Width, stored.Height)
	}
}
```

Check the file's imports already include `encoding/json`, `os`, `path/filepath`, `strings`, `net/http`, `net/http/httptest`, `server/internal/models` (they do per lines 3-20).

- [ ] **Step 8: Run all server tests and vet**

Run: `go test ./... && go vet ./...`
Expected: pass. The integration test skips without `CMS_TEST_DSN`; run it with a local MariaDB if available: `CMS_TEST_DSN='...' go test ./internal/handlers/ -run MediaHTTP_AcceptsMP3 -v`.

- [ ] **Step 9: Commit**

```bash
git add internal/mediatype internal/handlers/media.go internal/handlers/media_test.go internal/handlers/media_integration_test.go
git commit -m "feat(server): accept MP3 uploads with a frame-header fallback"
```

---

### Task 4: Sideload accepts audio with a longer timeout

**Files:**
- Modify: `server/internal/handlers/media.go:62` (new constant), `:371` (Accept header), `:373` (client call), `:433-446` (`remoteImageClient` signature)
- Modify: `server/internal/handlers/media_test.go`

**Interfaces:**
- Produces: `func remoteFetchTimeoutFor(target *url.URL) time.Duration`; `remoteImageClient(timeout time.Duration) *http.Client`.

- [ ] **Step 1: Write the failing test**

Append to `media_test.go` (add `"net/url"` and `"time"` to its imports):

```go
func TestRemoteFetchTimeoutFor(t *testing.T) {
	cases := map[string]time.Duration{
		"https://x.test/a.jpg":         remoteFetchTimeout,
		"https://x.test/a.mp3":         remoteAudioFetchTimeout,
		"https://x.test/A.MP3?v=1":     remoteAudioFetchTimeout,
		"https://x.test/a.mp3.html":    remoteFetchTimeout,
		"https://x.test/dir.mp3/a.jpg": remoteFetchTimeout,
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := remoteFetchTimeoutFor(u); got != want {
			t.Errorf("%s: got %v want %v", raw, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/handlers/ -run TestRemoteFetchTimeoutFor`
Expected: compile error, undefined `remoteFetchTimeoutFor`.

- [ ] **Step 3: Implement**

In `media.go`, after the `remoteFetchTimeout` constant (line 62), add:

```go
	// remoteAudioFetchTimeout replaces remoteFetchTimeout when the sideload
	// target looks like an MP3. 90 MiB in 30 s needs 3 MB/s from the remote,
	// which a podcast host will not always give.
	remoteAudioFetchTimeout = 5 * time.Minute
```

Before `remoteImageClient`, add:

```go
// remoteFetchTimeoutFor picks the sideload timeout from the target path's
// extension. The query is not part of url.Path, so "?v=1" does not confuse it.
func remoteFetchTimeoutFor(target *url.URL) time.Duration {
	if strings.EqualFold(path.Ext(target.Path), ".mp3") {
		return remoteAudioFetchTimeout
	}
	return remoteFetchTimeout
}
```

Change `func remoteImageClient() *http.Client {` to `func remoteImageClient(timeout time.Duration) *http.Client {` and inside it `Timeout:   remoteFetchTimeout,` to `Timeout:   timeout,`.

In `PostMediaFetch`, change `req.Header.Set("Accept", "image/*")` to `req.Header.Set("Accept", "image/*, audio/mpeg")` and `remoteImageClient().Do(req)` to `remoteImageClient(remoteFetchTimeoutFor(target)).Do(req)`.

The three user-facing strings in that handler say "image" ("could not fetch the image from that URL", "could not read the image from that URL", "image exceeds maximum size"). Change each "image" to "file".

- [ ] **Step 4: Run**

Run: `go test ./... && go vet ./...`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/handlers/media.go internal/handlers/media_test.go
git commit -m "feat(server): sideload MP3s with a timeout that fits their size"
```

---

### Task 5: Indexer adopts MP3 files only when their header checks out

**Files:**
- Modify: `server/internal/database/media.go:38-45` (extension map), `:447-451` (adoption check)
- Create: `server/internal/database/media_audio_test.go`

**Interfaces:**
- Consumes: `mediatype.IsMPEGAudioFrameHeader`.
- Produces: `func isAdoptableAudio(absPath string) bool` (package-private).

- [ ] **Step 1: Write the failing test**

Create `server/internal/database/media_audio_test.go`:

```go
package database

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIsAdoptableAudio(t *testing.T) {
	dir := t.TempDir()
	id3 := append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 64)...)
	frame := append([]byte{0xFF, 0xFB, 0x90, 0x64}, make([]byte, 64)...)
	text := []byte("not audio at all")

	if !isAdoptableAudio(writeFile(t, dir, "a.mp3", id3)) {
		t.Error("ID3-tagged file should be adoptable")
	}
	if !isAdoptableAudio(writeFile(t, dir, "b.mp3", frame)) {
		t.Error("bare-frame file should be adoptable")
	}
	if isAdoptableAudio(writeFile(t, dir, "c.mp3", text)) {
		t.Error("text named .mp3 should not be adoptable")
	}
	if isAdoptableAudio(filepath.Join(dir, "missing.mp3")) {
		t.Error("missing file should not be adoptable")
	}
}
```

- [ ] **Step 2: Run to see it fail**

Run: `go test ./internal/database/ -run TestIsAdoptableAudio`
Expected: compile error, undefined `isAdoptableAudio`.

- [ ] **Step 3: Implement**

In `server/internal/database/media.go`, add `".mp3": "audio/mpeg",` to `mediaMimeByExt` (after the `".svg"` line). Add `"io"`, `"net/http"` and `"server/internal/mediatype"` to the imports if absent.

Add this function near `IndexMediaRootWithProgress`:

```go
// isAdoptableAudio opens an .mp3 found on disk and applies the same check the
// upload path does. Images are adopted on extension alone because opening every
// file in the migrated corpus is too slow; audio files are few, and the
// extension is the only thing a stray non-audio file named .mp3 would have.
func isAdoptableAudio(absPath string) bool {
	f, err := os.Open(absPath)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && n == 0 {
		return false
	}
	head = head[:n]
	return http.DetectContentType(head) == "audio/mpeg" || mediatype.IsMPEGAudioFrameHeader(head)
}
```

In the walk callback, after `if !ok { return nil }` (line 450), add:

```go
		if mimeType == "audio/mpeg" && !isAdoptableAudio(absPath) {
			return nil
		}
```

- [ ] **Step 4: Run**

Run: `go test ./... && go vet ./...`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add internal/database/media.go internal/database/media_audio_test.go
git commit -m "feat(server): index on-disk MP3s after checking their header"
```

---

### Task 6: Shared URL parser in the frontend

**Files:**
- Create: `frontend/src/lib/embedUrl.ts`
- Create: `frontend/src/lib/embed-vectors.json`
- Create: `frontend/src/lib/embedUrl.test.ts`

**Interfaces:**
- Produces, exactly as the Scalene plan's Task 2 defines them: `EmbedProvider`, `Embed`, `ParseResult`, `parseEmbedUrl`, `parseAudioUrl`, `encodeShortcodeUrl`, `embedToShortcode`, `shortcodeToEmbed`, `audioToShortcode`, `shortcodeToAudio`.

- [ ] **Step 1: Copy the two files from the Scalene branch**

```bash
cp /Users/eden/Documents/code/triangle/Scalene/src/utils/embedUrl.ts src/lib/embedUrl.ts
cp /Users/eden/Documents/code/triangle/Scalene/src/utils/embed-vectors.json src/lib/embed-vectors.json
cmp src/lib/embedUrl.ts /Users/eden/Documents/code/triangle/Scalene/src/utils/embedUrl.ts && echo identical
```

If the Scalene branch does not have them yet (its Task 2 not done), write them from the Scalene plan's Task 2 Steps 1 and 4 verbatim, then copy them back to Scalene when that task runs. The content is the single source; it is not repeated here on purpose, so there is exactly one place to edit.

- [ ] **Step 2: Copy the test, adjusting only the import paths**

```bash
sed 's#"./embed-vectors.json"#"./embed-vectors.json"#; s#"./embedUrl"#"./embedUrl"#' \
  /Users/eden/Documents/code/triangle/Scalene/src/utils/embedUrl.test.ts > src/lib/embedUrl.test.ts
```

The paths are the same relative names, so the `sed` is a no-op guard; the file copies cleanly. The frontend's eslint config may want double quotes and no semicolons (it does: the existing `imageVariants.test.ts` uses that style). Run `npm run lint -- --fix src/lib/embedUrl.test.ts` and accept the formatting; do not lint-fix `embedUrl.ts` itself, since that file must stay identical to Scalene's copy. If lint rejects it, add it to the eslint ignore list with a comment saying why.

- [ ] **Step 3: Run**

Run: `npm test -- embedUrl`
Expected: all pass under jsdom.

- [ ] **Step 4: Commit**

```bash
git add src/lib/embedUrl.ts src/lib/embed-vectors.json src/lib/embedUrl.test.ts
git commit -m "feat(frontend): add shared embed URL parser and vectors"
```

---

### Task 7: Trix type declarations, permitted attributes and caption hiding

**Files:**
- Modify: `frontend/src/trix.d.ts:7-17` (attributes), `:108-115` (`TrixAttachment`)
- Modify: `frontend/src/components/TrixEditor.tsx:41-43` (permitted attributes)
- Modify: `frontend/src/components/TrixEditor.css` (after line 285)

**Interfaces:**
- Produces: `TrixAttachmentAttributes` gains `content?: string; embedProvider?: string; embedUrl?: string; embedOriginal?: string`. `TrixAttachment` gains `isPending(): boolean`. Constant `EMBED_CONTENT_TYPE = "application/x-triangle-embed"` exported from `trixArticleHtml.ts` (added in Task 8; declare it there, import it here).

- [ ] **Step 1: Extend the declarations**

In `trix.d.ts`, inside `interface TrixAttachmentAttributes`, after `previewable?: boolean;` add:

```ts
    // A content attachment renders this HTML instead of a preview. Our embed
    // cards use it; the card is always rebuilt from the two fields below and
    // never read back from storage.
    content?: string;
    embedProvider?: string;
    embedUrl?: string;
    // For a legacy shortcode loaded from an article: the original block, so an
    // unedited embed saves back byte-for-byte.
    embedOriginal?: string;
```

Inside `interface TrixAttachment`, after `remove(): void;` add:

```ts
    // True while a File is attached and no URL has arrived from the upload.
    isPending(): boolean;
```

- [ ] **Step 2: Permit the attributes on the piece**

In `TrixEditor.tsx`, replace lines 41-43:

```ts
  if (!window.Trix.AttachmentPiece.permittedAttributes.includes("align")) {
    window.Trix.AttachmentPiece.permittedAttributes.push("align");
  }
```

with:

```ts
  for (const name of ["align", "embedProvider", "embedUrl", "embedOriginal"]) {
    if (!window.Trix.AttachmentPiece.permittedAttributes.includes(name)) {
      window.Trix.AttachmentPiece.permittedAttributes.push(name);
    }
  }
```

- [ ] **Step 3: Hide the caption field for embeds and audio**

Trix renders every attachment figure with `data-trix-content-type` set to the attachment's content type (`trix.esm.js:3466`, `getData()`), so CSS can key on it. Append to `TrixEditor.css`:

```css
/* Embed cards and audio chips serialize to shortcodes, which have no caption.
   Hide the field so nothing can be typed there and then dropped on save. */
trix-editor.trix-content figure[data-trix-content-type="application/x-triangle-embed"] .attachment__caption,
trix-editor.trix-content figure[data-trix-content-type^="audio/"] .attachment__caption {
  display: none;
}

/* The embed card itself. Static: no iframe, no third-party script in the
   admin app. */
trix-editor.trix-content .embed-card {
  display: block;
  padding: 12px 16px;
  border: 1px dashed hsl(var(--border, 214 32% 91%));
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: hsl(var(--muted-foreground, 215 16% 47%));
}
trix-editor.trix-content .embed-card strong {
  color: hsl(var(--foreground, 222 47% 11%));
}
trix-editor.trix-content .embed-card--invalid {
  border-color: hsl(var(--destructive, 0 84% 60%));
  color: hsl(var(--destructive, 0 84% 60%));
}
```

- [ ] **Step 4: Type-check**

Run: `npx tsc -b`
Expected: 0 errors.

- [ ] **Step 5: Commit**

```bash
git add src/trix.d.ts src/components/TrixEditor.tsx src/components/TrixEditor.css
git commit -m "feat(frontend): declare embed attachment attributes and card styles"
```

---

### Task 8: Serialization branches, legacy round trip and corpus fixture

**Files:**
- Modify: `frontend/src/components/trixArticleHtml.ts`
- Modify: `frontend/src/components/trixArticleHtml.test.ts`
- Create: `frontend/scripts/extract-legacy-shortcodes.py`
- Create: `frontend/src/components/__fixtures__/legacy-shortcodes.json`

**Interfaces:**
- Consumes: `parseEmbedUrl`, `parseAudioUrl`, `embedToShortcode`, `shortcodeToEmbed`, `audioToShortcode`, `shortcodeToAudio` from `../lib/embedUrl`.
- Produces:
  - `export const EMBED_CONTENT_TYPE = "application/x-triangle-embed"`
  - `export const buildEmbedCardHtml(provider: string, url: string): string` (DOM-built, returns `outerHTML` of a `<div class="embed-card">`)
  - `export const buildInvalidCardHtml(): string`
  - `export const isAcceptableAudioUrl(url: string): string | null` (editor-side audio rule: every check except origin, because the editor has no `MEDIA_BASE_URL`; the URL always came from the server's own upload response or library row, and Scalene enforces origin)
  - `export const findInvalidEmbeds(articleHtml: string): string[]` (human messages, one per invalid block, for the save guard in Task 11)
  - `articleHtmlToTrix` and `trixHtmlToArticle` keep their signatures.

- [ ] **Step 1: Write the extraction script and run it against the WordPress export**

Create `frontend/scripts/extract-legacy-shortcodes.py`:

```python
#!/usr/bin/env python3
"""Extract every distinct legacy [youtube]/[soundcloud] block from a WordPress
export so the editor's round-trip test runs against the real corpus.

Usage: python3 scripts/extract-legacy-shortcodes.py /path/to/wp-posts.xml
Writes src/components/__fixtures__/legacy-shortcodes.json.
"""
import json
import re
import sys
from collections import Counter

SHORTCODE = re.compile(r"\[(youtube|soundcloud)\b[^\]]*\]", re.I)
BLOCK = re.compile(r"^<(p|div)(\s[^>]*)?>\s*(\[[^\]]*\])\s*</\1>$", re.I)


def main(path: str) -> None:
    text = open(path, encoding="utf-8", errors="ignore").read()
    bodies = re.findall(r"<content:encoded><!\[CDATA\[(.*?)\]\]></content:encoded>", text, re.S)
    occurrences = Counter()
    blocks = {}
    inline = 0
    for body in bodies:
        for line in body.split("\n"):
            line = line.strip()
            if not SHORTCODE.search(line):
                continue
            m = BLOCK.match(line)
            if m:
                occurrences["block"] += 1
                blocks.setdefault(line, {"block": line, "wrapper": m.group(1).lower()})
            elif SHORTCODE.fullmatch(line):
                occurrences["bare"] += 1
                blocks.setdefault(line, {"block": line, "wrapper": "bare"})
            else:
                inline += 1
    out = {
        "source": "wp-posts.xml export, see docs/superpowers/specs/2026-10-06-article-embeds-design.md section 2",
        "occurrences": dict(occurrences),
        "inlineWithProse": inline,
        "distinct": sorted(blocks.values(), key=lambda b: b["block"]),
    }
    with open("src/components/__fixtures__/legacy-shortcodes.json", "w", encoding="utf-8") as f:
        json.dump(out, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(json.dumps({k: v for k, v in out.items() if k != "distinct"}), "distinct:", len(out["distinct"]))


if __name__ == "__main__":
    main(sys.argv[1])
```

Run from `frontend/`:

```bash
mkdir -p src/components/__fixtures__
unzip -p /Users/eden/Documents/code/triangle/wordpress-etl/Data/wp-export.zip wp-posts.xml > /tmp/wp-posts.xml
python3 scripts/extract-legacy-shortcodes.py /tmp/wp-posts.xml
```

Expected output: occurrences near `{"block": 162, "bare": 79}` and `inlineWithProse` near 5, matching the spec's section 2 counts; distinct count somewhere between 60 and 250. Commit the JSON as produced. If the numbers differ by more than a handful, stop and check the regexes against the spec before continuing.

- [ ] **Step 2: Write the failing tests**

Append to `trixArticleHtml.test.ts`:

```ts
import legacy from "./__fixtures__/legacy-shortcodes.json"
import {
  EMBED_CONTENT_TYPE,
  buildEmbedCardHtml,
  findInvalidEmbeds,
  isAcceptableAudioUrl,
} from "./trixArticleHtml"
import { shortcodeToEmbed } from "../lib/embedUrl"

const SPOTIFY = "https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5"
const SPOTIFY_SC = `[spotify url="${SPOTIFY}" /]`
const AUDIO = "https://media.test/wp-content/uploads/2026/10/ep1.mp3"

const embedFigure = (data: Record<string, unknown>) =>
  `<div><figure data-trix-attachment='${JSON.stringify(data)}' data-trix-attributes='{}'><div class="embed-card">stored</div></figure></div>`

const readAttachment = (html: string) => {
  const m = /data-trix-attachment='([^']*)'/.exec(html)
  return m ? (JSON.parse(m[1].replace(/&quot;/g, '"')) as Record<string, unknown>) : null
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
})

describe("save: embed cards become shortcodes", () => {
  it("emits the canonical shortcode in its own div", () => {
    const html = articleHtmlToTrix(`<div>${SPOTIFY_SC}</div>`)
    expect(trixHtmlToArticle(html)).toBe(`<div>${SPOTIFY_SC}</div>`)
  })

  it("emits a legacy block byte-for-byte when unedited", () => {
    const block = `<p class="lead">[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;w=560&amp;h=315]</p>`
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

  it("reports nothing for a clean article", () => {
    expect(findInvalidEmbeds(`<div>${SPOTIFY_SC}</div><p>x</p>`)).toEqual([])
  })
})

describe("legacy corpus round trip", () => {
  for (const entry of legacy.distinct) {
    const embed = shortcodeToEmbed(/\[[^\]]*\]/.exec(entry.block)?.[0] ?? "")
    if (!embed) continue
    it(`keeps ${entry.block.slice(0, 70)} unchanged`, () => {
      expect(trixHtmlToArticle(articleHtmlToTrix(entry.block))).toBe(entry.block)
    })
  }
})

describe("isAcceptableAudioUrl", () => {
  it("accepts https under uploads with .mp3", () => {
    expect(isAcceptableAudioUrl(AUDIO + "?v=1")).toBe(AUDIO)
  })
  it("rejects blob, http and non-mp3", () => {
    expect(isAcceptableAudioUrl("blob:https://cms/abc")).toBeNull()
    expect(isAcceptableAudioUrl("http://media.test/wp-content/uploads/a.mp3")).toBeNull()
    expect(isAcceptableAudioUrl("https://media.test/wp-content/uploads/a.wav")).toBeNull()
  })
})

describe("buildEmbedCardHtml", () => {
  it("escapes the URL as text", () => {
    const html = buildEmbedCardHtml("youtube", `https://www.youtube.com/watch?v=<b>`)
    expect(html).toContain("&lt;b&gt;")
    expect(html).not.toContain("<b>")
  })
})
```

Note for the fixture loop: bare-text entries (`wrapper: "bare"`) are not converted by the load pass, so `articleHtmlToTrix` returns them unchanged and the round trip holds trivially; the loop still asserts it.

- [ ] **Step 3: Run to see them fail**

Run: `npm test -- trixArticleHtml`
Expected: FAIL, missing exports.

- [ ] **Step 4: Implement**

In `trixArticleHtml.ts`, add the import:

```ts
import {
  audioToShortcode,
  embedToShortcode,
  parseAudioUrl,
  parseEmbedUrl,
  shortcodeToAudio,
  shortcodeToEmbed,
  type Embed,
} from "../lib/embedUrl"
```

After `isImageContentType`, add:

```ts
export const EMBED_CONTENT_TYPE = "application/x-triangle-embed"

const isAudioContentType = (contentType: string): boolean => contentType.startsWith("audio/")

const PROVIDER_LABEL: Record<string, string> = {
  youtube: "YouTube",
  soundcloud: "SoundCloud",
  spotify: "Spotify",
  tiktok: "TikTok",
  instagram: "Instagram",
}

/**
 * The editor-side audio rule. Everything Scalene checks except the origin:
 * the editor has no MEDIA_BASE_URL, and the URL only ever came from the
 * server's upload response or a library row. Passing the URL's own origin as
 * the base makes parseAudioUrl check scheme, userinfo, port, path prefix and
 * extension and skip the origin comparison.
 */
export const isAcceptableAudioUrl = (url: string): string | null => {
  let origin: string
  try {
    origin = new URL(url).origin
  } catch {
    return null
  }
  return parseAudioUrl(url, origin)
}

/** The static preview card. Built with DOM APIs so the URL is always text. */
export const buildEmbedCardHtml = (provider: string, url: string): string => {
  const card = document.createElement("div")
  card.className = "embed-card"
  const label = document.createElement("strong")
  label.textContent = `${PROVIDER_LABEL[provider] ?? provider} embed`
  const link = document.createElement("div")
  link.textContent = url
  const note = document.createElement("div")
  note.textContent = "The player renders on the live site."
  card.append(label, link, note)
  return card.outerHTML
}

export const buildInvalidCardHtml = (): string => {
  const card = document.createElement("div")
  card.className = "embed-card embed-card--invalid"
  card.textContent = "Invalid embed. Remove this block and insert it again."
  return card.outerHTML
}

/** One wrapper element around exactly one shortcode, or a bare shortcode. */
const ORIGINAL_BLOCK_SHAPE = /^(?:<(p|div)(?:\s[^>]*)?>\s*\[[^\]]*\]\s*<\/\1>|\[[^\]]*\])$/i

const buildEmbedFigure = (doc: Document, embed: Embed, original?: string): HTMLElement => {
  const data: AttachmentData & { embedProvider: string; embedUrl: string; embedOriginal?: string; content: string } = {
    contentType: EMBED_CONTENT_TYPE,
    embedProvider: embed.provider,
    embedUrl: embed.url,
    content: buildEmbedCardHtml(embed.provider, embed.url),
  }
  if (original) data.embedOriginal = original
  const figure = doc.createElement("figure")
  figure.setAttribute("data-trix-attachment", JSON.stringify(data))
  figure.setAttribute("data-trix-attributes", "{}")
  figure.innerHTML = data.content
  return figure
}

const buildInvalidFigure = (doc: Document): HTMLElement => {
  const figure = doc.createElement("figure")
  const content = buildInvalidCardHtml()
  figure.setAttribute("data-trix-attachment", JSON.stringify({ contentType: EMBED_CONTENT_TYPE, content }))
  figure.setAttribute("data-trix-attributes", "{}")
  figure.innerHTML = content
  return figure
}

const buildAudioFigure = (doc: Document, url: string): HTMLElement => {
  const data: AttachmentData = {
    contentType: "audio/mpeg",
    url,
    href: url,
    filename: url.split("/").pop() ?? "audio.mp3",
  }
  const figure = doc.createElement("figure")
  figure.setAttribute("data-trix-attachment", JSON.stringify(data))
  figure.setAttribute("data-trix-attributes", "{}")
  return figure
}

/** Wrap a figure in the <div> Trix uses for an attachment block. */
const wrapBlock = (doc: Document, figure: HTMLElement): HTMLElement => {
  const block = doc.createElement("div")
  block.appendChild(figure)
  return block
}
```

Extend `AttachmentData` with the new optional fields:

```ts
type AttachmentData = {
  contentType?: string
  url?: string
  href?: string
  filename?: string
  alt?: string
  width?: number
  height?: number
  previewable?: boolean
  content?: string
  embedProvider?: string
  embedUrl?: string
  embedOriginal?: string
}
```

Replace the start of `articleHtmlToTrix` so the two new passes run before the image pass. Change:

```ts
export const articleHtmlToTrix = (html: string): string => {
  if (!html.includes("<figure") && !html.includes("wp-caption")) return html
  const doc = parseDocument(html)
  if (!doc) return html

  let changed = false
```

to:

```ts
export const articleHtmlToTrix = (html: string): string => {
  if (!html.includes("<figure") && !html.includes("wp-caption") && !html.includes("[")) return html
  const doc = parseDocument(html)
  if (!doc) return html

  let changed = false

  // Pass 1: every stored embed or audio figure is rebuilt from its attributes.
  // Stored `content` is never rendered; a figure whose attributes do not
  // validate becomes an inert error card that the save guard refuses.
  for (const figure of Array.from(doc.querySelectorAll("figure[data-trix-attachment]"))) {
    const data = parseJSONAttribute<AttachmentData>(figure, "data-trix-attachment")
    const contentType = data?.contentType ?? ""
    if (contentType === EMBED_CONTENT_TYPE) {
      const parsed = parseEmbedUrl(data?.embedUrl ?? "")
      const valid = parsed.ok && parsed.embed.provider === data?.embedProvider
      figure.replaceWith(valid ? buildEmbedFigure(doc, parsed.embed, data?.embedOriginal) : buildInvalidFigure(doc))
      changed = true
    } else if (isAudioContentType(contentType)) {
      const url = isAcceptableAudioUrl(data?.url ?? "")
      figure.replaceWith(url ? buildAudioFigure(doc, url) : buildInvalidFigure(doc))
      changed = true
    }
  }

  // Pass 2: a top-level <p> or <div> whose whole content is one shortcode
  // becomes a card. Legacy blocks keep their original markup in embedOriginal
  // so an unedited article saves back unchanged. Text inline with prose, bare
  // text nodes and anything that does not parse stay as they are.
  for (const block of Array.from(doc.body.children)) {
    const tag = block.tagName.toLowerCase()
    if (tag !== "p" && tag !== "div") continue
    if (block.children.length > 0) continue
    const text = (block.textContent ?? "").trim()
    if (!text.startsWith("[")) continue
    const embed = shortcodeToEmbed(text)
    if (embed) {
      block.replaceWith(wrapBlock(doc, buildEmbedFigure(doc, embed, block.outerHTML)))
      changed = true
      continue
    }
    const audio = shortcodeToAudio(text, safeOrigin(text))
    if (audio) {
      block.replaceWith(wrapBlock(doc, buildAudioFigure(doc, audio)))
      changed = true
    }
  }
```

Add this helper near `isAcceptableAudioUrl` (it lets `shortcodeToAudio` run the editor-side rule by handing it the src's own origin):

```ts
/** The origin of the src inside an [audio] shortcode, or "" when unparsable. */
const safeOrigin = (shortcodeText: string): string => {
  const m = /\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)')/i.exec(shortcodeText)
  try {
    return new URL(m?.[1] ?? m?.[2] ?? "").origin
  } catch {
    return ""
  }
}
```

In `trixHtmlToArticle`, replace the single line

```ts
    if (data.contentType && !isImageContentType(data.contentType)) continue
```

with:

```ts
    if (data.contentType === EMBED_CONTENT_TYPE) {
      const parsed = parseEmbedUrl(data.embedUrl ?? "")
      if (!parsed.ok || parsed.embed.provider !== data.embedProvider) continue // invalid: left for findInvalidEmbeds
      const original = data.embedOriginal ?? ""
      if (original && ORIGINAL_BLOCK_SHAPE.test(original)) {
        const inner = /\[[^\]]*\]/.exec(original)?.[0] ?? ""
        const originalEmbed = shortcodeToEmbed(inner)
        if (originalEmbed && originalEmbed.url === parsed.embed.url) {
          // Unedited legacy block: emit it exactly as it was stored. The shape
          // check above has already constrained it to one wrapper element
          // around one shortcode, so this is not an open HTML channel.
          const holder = doc.createElement("div")
          holder.innerHTML = original
          const replacement = holder.firstChild
          if (replacement) {
            ;(figure.parentElement?.tagName === "DIV" && figure.parentElement.childNodes.length === 1
              ? figure.parentElement
              : figure
            ).replaceWith(replacement)
            changed = true
            continue
          }
        }
      }
      const out = doc.createElement("div")
      out.textContent = embedToShortcode(parsed.embed)
      replaceAttachmentBlock(figure, out)
      changed = true
      continue
    }
    if (data.contentType && isAudioContentType(data.contentType)) {
      const url = isAcceptableAudioUrl(data.url ?? "")
      if (!url) continue // still uploading or invalid: left for findInvalidEmbeds
      const out = doc.createElement("div")
      out.textContent = audioToShortcode(url)
      replaceAttachmentBlock(figure, out)
      changed = true
      continue
    }
    if (data.contentType && !isImageContentType(data.contentType)) continue
```

Also change the function's early return so it still runs for these figures: the first line `if (!html.includes("data-trix-attachment")) return html` already covers them.

Add the helper used above, before `trixHtmlToArticle`:

```ts
/**
 * Trix wraps an attachment in its own <div> block. The shortcode must be the
 * whole content of a top-level block, so when the figure is the only child of
 * that <div>, replace the <div>; otherwise replace just the figure.
 */
const replaceAttachmentBlock = (figure: Element, replacement: Element): void => {
  const parent = figure.parentElement
  if (parent && parent.tagName === "DIV" && parent.childNodes.length === 1 && parent.parentElement?.tagName === "BODY") {
    parent.replaceWith(replacement)
  } else {
    figure.replaceWith(replacement)
  }
}
```

Finally add `findInvalidEmbeds` at the end of the file:

```ts
/**
 * Human messages for every embed or audio attachment that cannot be saved:
 * invalid attributes, or an upload that has not finished. Runs over the
 * converted article HTML, where such figures are passed through untouched.
 */
export const findInvalidEmbeds = (articleHtml: string): string[] => {
  if (!articleHtml.includes("data-trix-attachment")) return []
  const doc = parseDocument(articleHtml)
  if (!doc) return []
  const messages: string[] = []
  let embeds = 0
  let audios = 0
  for (const figure of Array.from(doc.querySelectorAll("figure[data-trix-attachment]"))) {
    const data = parseJSONAttribute<AttachmentData>(figure, "data-trix-attachment")
    const contentType = data?.contentType ?? ""
    if (contentType === EMBED_CONTENT_TYPE) {
      embeds += 1
      messages.push(`Embed ${embeds} has an invalid URL. Remove the block and insert it again.`)
    } else if (isAudioContentType(contentType)) {
      audios += 1
      const url = data?.url ?? ""
      messages.push(
        url.startsWith("blob:") || url.startsWith("data:") || !url
          ? `Audio ${audios} has not finished uploading.`
          : `Audio ${audios} has an invalid URL. Remove the block and upload it again.`,
      )
    }
  }
  return messages
}
```

- [ ] **Step 5: Run**

Run: `npm test -- trixArticleHtml`
Expected: all pass, including every corpus entry. Likely first failures and their fixes:
- The `&amp;` legacy block: `block.textContent` decodes `&amp;` to `&`, `shortcodeToEmbed` parses it, and `embedOriginal` holds the raw `outerHTML` with `&amp;`, which is what gets emitted. If jsdom serializes `outerHTML` differently from the input (attribute quoting), the fixture loop will show which entries; the strict-shape regex still accepts them, and the test compares against `entry.block`, so adjust the extraction script to store `outerHTML`-normalized blocks rather than relax the test.
- The `embedFigure` helper's `data-trix-attachment` uses single quotes around JSON that contains `"`; jsdom parses that fine.

- [ ] **Step 6: Lint and type-check**

Run: `npm run lint && npx tsc -b`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add src/components/trixArticleHtml.ts src/components/trixArticleHtml.test.ts scripts/extract-legacy-shortcodes.py src/components/__fixtures__/legacy-shortcodes.json
git commit -m "feat(frontend): convert embed and audio attachments to and from shortcodes"
```

---

### Task 9: Vendor-independent editor API surface

This task is small on purpose: it adds the hooks Tasks 10 and 11 plug into, so each of those can be reviewed alone.

**Files:**
- Modify: `frontend/src/components/TrixEditor.tsx:90-95` (props), and the `trix-change` handler at `:154-163`

**Interfaces:**
- Produces:
  - `export type TrixEditorApi = { hasPendingUploads(): boolean }`
  - `TrixEditorProps` gains `apiRef?: React.MutableRefObject<TrixEditorApi | null>` and `onPendingChange?: (pending: boolean) => void`.

- [ ] **Step 1: Add the types and props**

Replace lines 90-95:

```ts
type TrixEditorProps = {
  value: string;
  onChange: (html: string) => void;
};

function TrixEditor({ value, onChange }: TrixEditorProps) {
```

with:

```ts
export type TrixEditorApi = {
  // True while any attachment has a File and no URL yet, which is Trix's own
  // definition of an upload in flight (Attachment#isPending).
  hasPendingUploads(): boolean;
};

type TrixEditorProps = {
  value: string;
  onChange: (html: string) => void;
  // Lets the page ask about upload state synchronously at save time.
  apiRef?: React.MutableRefObject<TrixEditorApi | null>;
  // Fired on every change so the page can disable its save button as a cue.
  onPendingChange?: (pending: boolean) => void;
};

function TrixEditor({ value, onChange, apiRef, onPendingChange }: TrixEditorProps) {
```

Add `import type React from "react";` is unnecessary: `React.MutableRefObject` resolves through the existing `react` import only if `React` is imported as a namespace. Use `import { type MutableRefObject, ... } from "react"` instead: change the first import line to

```ts
import { useCallback, useEffect, useId, useRef, useState, type MutableRefObject } from "react";
```

and write the prop type as `MutableRefObject<TrixEditorApi | null>`.

- [ ] **Step 2: Implement the API inside the component**

After the `altEditor` state (line 136), add:

```ts
  const hasPendingUploads = useCallback((): boolean => {
    const editor = editorRef.current;
    if (!editor) return false;
    return editor.editor.getDocument().getAttachments().some((a) => a.isPending());
  }, []);

  useEffect(() => {
    if (!apiRef) return;
    apiRef.current = { hasPendingUploads };
    return () => {
      apiRef.current = null;
    };
  }, [apiRef, hasPendingUploads]);
```

In the `trix-change` handler (the `handleChange` inside the effect at lines 154-163), after `onChange(html);` add:

```ts
      onPendingChange?.(hasPendingUploads());
```

and add `hasPendingUploads` and `onPendingChange` to that effect's dependency array.

- [ ] **Step 3: Add a test**

Append to `src/pages/editArticleView.test.tsx` a focused test of the API through the real editor (the file already renders `EditArticleView` with Trix in jsdom). Place it in its own `describe` at the end:

```ts
describe("TrixEditor api", () => {
  it("reports no pending uploads on a loaded article", async () => {
    const { default: TrixEditor } = await import("../components/TrixEditor")
    type Api = import("../components/TrixEditor").TrixEditorApi
    // A plain object satisfies MutableRefObject; no React import needed here.
    const apiRef: { current: Api | null } = { current: null }
    render(<TrixEditor value="<p>Body</p>" onChange={() => {}} apiRef={apiRef} />)
    await waitFor(() => expect(apiRef.current).not.toBeNull())
    expect(apiRef.current?.hasPendingUploads()).toBe(false)
  })
})
```

- [ ] **Step 4: Run**

Run: `npm test -- editArticleView && npx tsc -b`
Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add src/components/TrixEditor.tsx src/pages/editArticleView.test.tsx
git commit -m "feat(frontend): expose upload state from the editor"
```

---

### Task 10: Embed button, URL prompt, audio uploads and paste handling in the editor

**Files:**
- Modify: `frontend/src/components/TrixEditor.tsx`: imports (`:8-12`), upload handler (`:287-296`), attachment-add (`:352-370`), library insert (`:410-445`), file-accept (`:451-466`), toolbar (`:1569-1583`), dialogs (after the `AltTextDialog` definition at `:1702`)
- Modify: `frontend/src/pages/editArticleView.test.tsx`

**Interfaces:**
- Consumes: `EMBED_CONTENT_TYPE`, `buildEmbedCardHtml` from `./trixArticleHtml`; `parseEmbedUrl` from `../lib/embedUrl`; `openBlockForImage`, `failAttachment`, `setNotice`, `savedRangeRef` already in the component.
- Produces: an "Embed" toolbar button (`title="Embed a link (YouTube, SoundCloud, Spotify, TikTok, Instagram)"`), an `EmbedDialog` component, and `insertEmbed(url: string)`.

- [ ] **Step 1: Write the failing tests**

Append to `src/pages/editArticleView.test.tsx`, inside the `TrixEditor api` describe from Task 9 or a new one:

```ts
describe("embeds in the editor", () => {
  const renderEditor = async (value = "<p>Body</p>") => {
    const { default: TrixEditor } = await import("../components/TrixEditor")
    const onChange = vi.fn()
    render(<TrixEditor value={value} onChange={onChange} />)
    await waitFor(() => expect(document.querySelector("trix-editor")).not.toBeNull())
    return { onChange }
  }

  it("inserts a pasted YouTube link as a card in its own block", async () => {
    const user = userEvent.setup()
    const { onChange } = await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://youtu.be/dQw4w9WgXcQ")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    await waitFor(() => {
      const last = String(onChange.mock.calls.at(-1)?.[0] ?? "")
      expect(last).toContain("[youtube https://www.youtube.com/watch?v=dQw4w9WgXcQ]")
    })
    const last = String(onChange.mock.calls.at(-1)?.[0])
    expect(last).toMatch(/<div>\[youtube [^\]]+\]<\/div>/)
    expect(last).toContain("<div>Body</div>")
  })

  it("refuses a short link with a specific message and inserts nothing", async () => {
    const user = userEvent.setup()
    const { onChange } = await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://vm.tiktok.com/ZMabc/")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    expect(await screen.findByText(/open the link in a browser and paste the full url/i)).toBeInTheDocument()
    expect(onChange.mock.calls.flat().join("")).not.toContain("[tiktok")
  })

  it("refuses an unsupported host and names the accepted ones", async () => {
    const user = userEvent.setup()
    await renderEditor()
    await user.click(screen.getByTitle(/embed a link/i))
    await user.type(screen.getByLabelText(/link to embed/i), "https://vimeo.com/123")
    await user.click(screen.getByRole("button", { name: /^insert$/i }))
    expect(await screen.findByText(/youtube, soundcloud, spotify, tiktok or instagram/i)).toBeInTheDocument()
  })

  it("refuses a dropped .wav before any upload starts", async () => {
    await renderEditor()
    const editor = document.querySelector("trix-editor") as HTMLElement
    const event = new Event("trix-file-accept", { bubbles: true, cancelable: true }) as Event & { file: File }
    event.file = new File([new Uint8Array(8)], "clip.wav", { type: "audio/wav" })
    editor.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(await screen.findByText(/only images and mp3/i)).toBeInTheDocument()
  })

  it("accepts an .mp3 with an empty browser type", async () => {
    await renderEditor()
    const editor = document.querySelector("trix-editor") as HTMLElement
    const event = new Event("trix-file-accept", { bubbles: true, cancelable: true }) as Event & { file: File }
    event.file = new File([new Uint8Array(8)], "Episode.MP3", { type: "" })
    editor.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true) // our handler always prevents default and inserts itself
    expect(screen.queryByText(/only images and mp3/i)).toBeNull()
  })

  it("rebuilds the card of a pasted embed attachment", async () => {
    const { onChange } = await renderEditor()
    const editor = document.querySelector("trix-editor") as HTMLElement & { editor: { insertHTML(h: string): void } }
    const hostile = `<figure data-trix-attachment='${JSON.stringify({
      contentType: "application/x-triangle-embed",
      embedProvider: "spotify",
      embedUrl: "https://open.spotify.com/episode/7makk4oTQel546B0PZlDM5",
      content: "<img src=x onerror=alert(1)>",
    })}'></figure>`
    await act(async () => {
      editor.editor.insertHTML(hostile)
    })
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    expect(document.querySelector("trix-editor")?.innerHTML).not.toContain("onerror")
    expect(document.querySelector("trix-editor .embed-card")).not.toBeNull()
  })
})
```

- [ ] **Step 2: Run to see them fail**

Run: `npm test -- editArticleView -t "embeds in the editor"`
Expected: FAIL, no element with title matching "embed a link".

- [ ] **Step 3: Imports and the Embed dialog**

Change the import block at lines 8-12 to:

```ts
import {
  EMBED_CONTENT_TYPE,
  articleHtmlToTrix,
  buildEmbedCardHtml,
  contentTypeForUrl,
  trixHtmlToArticle,
} from "./trixArticleHtml";
import { parseEmbedUrl } from "../lib/embedUrl";
```

Add constants near `isOwnMediaUrl`:

```ts
const ACCEPTED_HOSTS_MESSAGE =
  "That link is not supported. Paste a link from YouTube, SoundCloud, Spotify, TikTok or Instagram.";
const SHORT_LINK_MESSAGE =
  "That is a short link. Open the link in a browser and paste the full URL.";
const FILE_TYPE_MESSAGE = "Only images and MP3 audio can be added to an article.";

const isAcceptedUploadFile = (file: File): boolean => {
  const type = file.type.toLowerCase();
  if (type.startsWith("image/")) return true;
  if (type === "audio/mpeg" || type === "audio/mp3" || type === "audio/mpeg3") return true;
  return type === "" && /\.mp3$/i.test(file.name);
};
```

After the `AltTextDialog` component (its definition starts at line 1702; add after its closing brace), add:

```tsx
type EmbedDialogProps = {
  onInsert: (url: string) => string | null; // returns an error message or null
  onClose: () => void;
};

function EmbedDialog({ onInsert, onClose }: EmbedDialogProps) {
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const inputId = useId();

  const submit = () => {
    const message = onInsert(draft);
    if (message) {
      setError(message);
      return;
    }
    onClose();
  };

  return (
    <div className="mt-2 flex flex-col gap-2 rounded-lg border border-border bg-card p-3" role="group" aria-label="Embed a link">
      <label className="text-xs font-semibold uppercase tracking-wide text-muted-foreground" htmlFor={inputId}>
        Link to embed
      </label>
      <input
        autoFocus
        className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/40"
        id={inputId}
        onChange={(e) => {
          setDraft(e.target.value);
          setError(null);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            submit();
          }
          if (e.key === "Escape") onClose();
        }}
        placeholder="https://www.youtube.com/watch?v=..."
        type="url"
        value={draft}
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="flex gap-2">
        <button className="rounded-lg bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground" onClick={submit} type="button">
          Insert
        </button>
        <button className="rounded-lg border border-border px-3 py-1.5 text-sm" onClick={onClose} type="button">
          Cancel
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: State, insertEmbed and the toolbar button**

Next to `const [pickerOpen, setPickerOpen] = useState(false);` add:

```ts
  const [embedOpen, setEmbedOpen] = useState(false);
```

After `insertFromLibrary`, add:

```ts
  const openEmbed = useCallback(() => {
    const editor = editorRef.current;
    savedRangeRef.current = editor ? editor.editor.getSelectedRange() : null;
    setEmbedOpen(true);
  }, []);

  // Returns an error message, or null after a successful insert.
  const insertEmbed = useCallback(
    (input: string): string | null => {
      const parsed = parseEmbedUrl(input);
      if (!parsed.ok) return parsed.reason === "short-link" ? SHORT_LINK_MESSAGE : ACCEPTED_HOSTS_MESSAGE;
      const editor = editorRef.current;
      const Trix = window.Trix;
      if (!editor || !Trix) return "The editor is not ready.";

      editor.focus();
      if (savedRangeRef.current) {
        editor.editor.setSelectedRange(savedRangeRef.current);
        savedRangeRef.current = null;
      }
      openBlockForImage();
      const { provider, url } = parsed.embed;
      editor.editor.insertAttachment(
        new Trix.Attachment({
          contentType: EMBED_CONTENT_TYPE,
          embedProvider: provider,
          embedUrl: url,
          content: buildEmbedCardHtml(provider, url),
        }),
      );
      return null;
    },
    [openBlockForImage],
  );
```

In the toolbar, inside the `trix-button-group--file-tools` span, after the library picker button (the one with `title="Insert image from library"`), add:

```tsx
            <button
              type="button"
              className="trix-button trix-button--icon-link"
              onClick={openEmbed}
              title="Embed a link (YouTube, SoundCloud, Spotify, TikTok, Instagram)"
              tabIndex={-1}
            >
              <svg className="trix-icon" aria-hidden="true">
                <use href={`#${toolbarId}-embed`} />
              </svg>
            </button>
```

Add an `embed` symbol next to the other toolbar SVG symbols (find where `${toolbarId}-image` is defined as a `<symbol id=...>` and add a sibling):

```tsx
          <symbol id={`${toolbarId}-embed`} viewBox="0 0 24 24">
            <path d="M4 6h16v12H4z" fill="none" stroke="currentColor" strokeWidth="2" />
            <path d="m10 9 5 3-5 3z" fill="currentColor" />
          </symbol>
```

Where the `MediaPicker` is rendered (line 1690 area), render the dialog beside it:

```tsx
      {embedOpen && <EmbedDialog onInsert={insertEmbed} onClose={() => setEmbedOpen(false)} />}
```

- [ ] **Step 5: File accept**

Replace the `handleFileAccept` body (lines 454-462) with:

```ts
    const handleFileAccept = (event: Event) => {
      const Trix = window.Trix;
      if (!Trix) return;
      event.preventDefault();
      const { file } = event as TrixFileAcceptEvent;
      if (!isAcceptedUploadFile(file)) {
        setNotice(FILE_TYPE_MESSAGE);
        return;
      }
      openBlockForImage();
      editor.editor.insertAttachment(Trix.Attachment.attachmentForFile(file));
    };
```

- [ ] **Step 6: Upload response sets the server's content type, and a timeout**

In `uploadFile`, replace

```ts
          const { url } = JSON.parse(xhr.responseText) as { url: string };
          attachment.setAttributes(
            file.type.startsWith("image/") ? { url } : { url, href: url },
          );
```

with

```ts
          const { url, content_type: contentType } = JSON.parse(xhr.responseText) as {
            url: string;
            content_type?: string;
          };
          // The server's sniffed type, not the browser's: browsers report MP3s
          // as audio/mpeg, audio/mp3, audio/mpeg3 or nothing at all, and the
          // save branch keys on the real type.
          const isImage = (contentType ?? file.type).startsWith("image/");
          attachment.setAttributes(
            isImage
              ? { url, ...(contentType ? { contentType } : {}) }
              : { url, href: url, ...(contentType ? { contentType } : {}) },
          );
```

Before `xhr.send(formData);` add:

```ts
      // A hung upload must not hold the save guard forever.
      xhr.timeout = 10 * 60 * 1000;
      xhr.ontimeout = () => {
        failAttachment(attachment, `Could not add ${file.name}: the upload timed out.`);
      };
```

- [ ] **Step 7: Paste and drop rebuild embed cards**

At the top of `handleAttachmentAdd`, before `if (attachment.file) {`, add:

```ts
      const contentType = String(attachment.getAttribute("contentType") ?? "");
      if (contentType === EMBED_CONTENT_TYPE) {
        // Pasted from another tab or article. Rebuild the card from validated
        // attributes; stored content is never rendered.
        const parsed = parseEmbedUrl(String(attachment.getAttribute("embedUrl") ?? ""));
        if (!parsed.ok || parsed.embed.provider !== attachment.getAttribute("embedProvider")) {
          attachment.remove();
          setNotice("A pasted embed was removed because its link is not valid.");
          return;
        }
        attachment.setAttributes({
          embedUrl: parsed.embed.url,
          content: buildEmbedCardHtml(parsed.embed.provider, parsed.embed.url),
        });
        return;
      }
      if (contentType.startsWith("audio/") && !attachment.file) {
        // A pasted audio chip. Anything not on our media infrastructure goes.
        const url = String(attachment.getAttribute("url") ?? "");
        if (!isOwnMediaUrl(url)) {
          attachment.remove();
          setNotice("A pasted audio file was removed because it is not in the media library.");
        }
        return;
      }
```

- [ ] **Step 8: Library insert for audio rows**

In `insertFromLibrary`, replace the `new Trix.Attachment({...})` construction with:

```ts
      const mimeType = item.mime_type || contentTypeForUrl(item.url);
      const attachment = mimeType.startsWith("audio/")
        ? new Trix.Attachment({
            url: item.url,
            href: item.url,
            contentType: mimeType,
            filename: item.file_name,
          })
        : new Trix.Attachment({
            url: item.url,
            contentType: mimeType,
            filename: item.alt_text || item.file_name,
            alt: item.alt_text ?? "",
            ...(item.width ? { width: item.width } : {}),
            ...(item.height ? { height: item.height } : {}),
            // Trix's previewablePattern excludes extension-less URLs, which would
            // render a library image as a file stub instead of a preview.
            previewable: true,
          });
      editor.editor.insertAttachment(attachment);

      if (!mimeType.startsWith("audio/") && !item.alt_text) {
```

(and keep the existing alt-text notice inside that `if`).

- [ ] **Step 9: Run**

Run: `npm test -- editArticleView && npm run lint && npx tsc -b`
Expected: all pass. If the "pasted embed" test does not see `.embed-card`, confirm that `insertHTML` fires `trix-attachment-add` in jsdom (Trix does so for attachments parsed from HTML); if it does not, drive the same path by constructing `new window.Trix.Attachment({...})` and calling `editor.editor.insertAttachment` in the test instead, which fires the event.

- [ ] **Step 10: Commit**

```bash
git add src/components/TrixEditor.tsx src/pages/editArticleView.test.tsx
git commit -m "feat(frontend): embed button, audio uploads and paste handling"
```

---

### Task 11: Save guard in the article page

**Files:**
- Modify: `frontend/src/pages/editArticleView.tsx`: state near `:285`, `saveArticle` at `:722-735`, the `TrixEditor` usage at `:1373`, the save button at `:1807-1808`
- Modify: `frontend/src/pages/editArticleView.test.tsx`

**Interfaces:**
- Consumes: `TrixEditorApi` and the `apiRef`/`onPendingChange` props from Task 9; `findInvalidEmbeds` from Task 8.

- [ ] **Step 1: Write the failing tests**

The existing test file renders `EditArticleView` against `apiFetchStub`. Add, in the existing publish-timing `describe` or a new one:

The file already has `renderEditor()` (line 108), which returns a `user` set up with fake timers, and a stub whose article GET returns `content: "<p>Body</p>"` (around line 60). Two harness edits first: add `let articleContent: string` beside the other `let` stubs, set `articleContent = "<p>Body</p>"` in the existing `beforeEach`, and change the stub to `content: articleContent`.

```ts
describe("save guard for embeds and uploads", () => {
  const nonGetCalls = () => apiCalls.filter((c) => c.method !== "GET").length

  it("disables Save and pauses autosave while an upload is pending, sending nothing", async () => {
    await renderEditor()
    const trix = document.querySelector("trix-editor") as HTMLElement & {
      editor: { insertAttachment(a: unknown): void }
    }
    const Trix = (window as unknown as { Trix: { Attachment: new (a: Record<string, unknown>) => unknown } }).Trix
    const pending = new Trix.Attachment({ file: new File([new Uint8Array(4)], "ep.mp3", { type: "audio/mpeg" }) })
    const before = nonGetCalls()
    await act(async () => {
      trix.editor.insertAttachment(pending)
    })
    await waitFor(() => expect(screen.getByRole("button", { name: /^save$/i })).toBeDisabled())
    // The content change schedules an autosave; the synchronous guard must refuse it.
    await act(async () => {
      vi.advanceTimersByTime(3000)
    })
    expect(await screen.findByText(/waiting for upload to finish/i)).toBeInTheDocument()
    expect(nonGetCalls()).toBe(before)
  })

  it("refuses to save an article with an invalid embed and names the block", async () => {
    articleContent = `<div><figure data-trix-attachment='${JSON.stringify({ contentType: "application/x-triangle-embed", embedProvider: "spotify", embedUrl: "nope" })}' data-trix-attributes='{}'></figure></div>`
    const user = await renderEditor()
    const before = nonGetCalls()
    await user.click(screen.getByRole("button", { name: /^save$/i }))
    expect(await screen.findByText(/embed 1 has an invalid url/i)).toBeInTheDocument()
    expect(nonGetCalls()).toBe(before)
  })
})
```

If the Save button's accessible name differs (it may read "Save draft" or "Publish"), read the JSX at line 1807 and use that name; the point is the explicit save path, not the publish transition.

- [ ] **Step 2: Run to see them fail**

Run: `npm test -- editArticleView -t "save guard"`
Expected: FAIL: the save proceeds (a non-GET call is made) or the message is missing.

- [ ] **Step 3: Implement**

In `editArticleView.tsx`:

Imports: add `import TrixEditor, { type TrixEditorApi } from "../components/TrixEditor"` (adjust the existing `TrixEditor` import) and `import { findInvalidEmbeds } from "../components/trixArticleHtml"`.

State, near line 285:

```ts
  const editorApiRef = useRef<TrixEditorApi | null>(null)
  const [hasPendingUploads, setHasPendingUploads] = useState(false)
```

In `saveArticle`, directly after the `validationError` helper is defined (line 733) and before `setSavingState(true)`, add:

```ts
    // Synchronous, on every path into a save: the button is only a cue.
    if (editorApiRef.current?.hasPendingUploads()) {
      validationError("Waiting for upload to finish before saving.", "Waiting for upload to finish.")
      return
    }
    const invalidEmbeds = findInvalidEmbeds(content)
    if (invalidEmbeds.length > 0) {
      validationError(invalidEmbeds[0], "Autosave paused: fix the invalid embed.")
      return
    }
```

`content` is the page's body state (the same value the save payload trims at lines 804 and 857), so the guard sees exactly what would be sent.

The editor usage at line 1373 becomes:

```tsx
              <TrixEditor
                apiRef={editorApiRef}
                onChange={setContent}
                onPendingChange={setHasPendingUploads}
                value={content}
              />
```

The save button at 1807: `disabled={isSaving}` becomes `disabled={isSaving || hasPendingUploads}` and, if there is a second button for publishing on the same lines, the same change there.

- [ ] **Step 4: Run**

Run: `npm test -- editArticleView && npx tsc -b`
Expected: pass. The first test pins both facts: the button is disabled as a cue, and the autosave path, which does not go through the button, is refused by the synchronous guard with the message shown. `AUTOSAVE_DELAY_MS` is 2500 (`editArticleView.tsx:253`), so advancing 3000 ms fires it.

- [ ] **Step 5: Commit**

```bash
git add src/pages/editArticleView.tsx src/pages/editArticleView.test.tsx
git commit -m "feat(frontend): refuse to save while uploads are pending or embeds are invalid"
```

---

### Task 12: Media picker accepts and shows audio; image-only pickers filter it out

**Files:**
- Modify: `frontend/src/components/MediaPicker.tsx:6-14` (item type), `:21-30` (props), `:60` (items), `:239` (accept), `:282-293` (tile)
- Modify: `frontend/src/pages/editArticleView.tsx:1845` and `frontend/src/pages/settingsPage.tsx:854` (pass `imagesOnly`)
- Modify: `frontend/src/components/MediaPicker.test.tsx`

**Interfaces:**
- Produces: `MediaPickerProps` gains `imagesOnly?: boolean`. When true, the picker asks the server for images only by adding `mime_type=image/` to its `/v1/media/gallery` query (the endpoint already supports it: `mediaListParams` at `media.go:705` reads `mime_type`, and a trailing slash matches the family per the swagger comment at `:750`), and the file input accepts images only. The body editor passes nothing; the featured-image and settings pickers pass `imagesOnly`. No server change. The public gallery endpoint already filters `image/` itself (`media.go:839-847`).

- [ ] **Step 1: Write the failing tests**

Append to `MediaPicker.test.tsx` (look at its existing stub for the gallery fetch and add an audio row to the payload it returns):

```ts
it("shows an audio row as an audio tile", async () => {
  // Extend the stubbed /v1/media payload with:
  // { id: 9, url: "https://m.test/wp-content/uploads/2026/10/ep.mp3", file_name: "ep.mp3", mime_type: "audio/mpeg" }
  render(<MediaPicker onClose={() => {}} onSelect={() => {}} />)
  expect(await screen.findByText("ep.mp3")).toBeInTheDocument()
  expect(screen.getByLabelText("Audio file")).toBeInTheDocument()
  expect(document.querySelector('img[src$="ep.mp3"]')).toBeNull()
})

it("asks the server for images only when imagesOnly is set", async () => {
  render(<MediaPicker imagesOnly onClose={() => {}} onSelect={() => {}} />)
  await waitFor(() => expect(apiFetchStub).toHaveBeenCalled())
  const url = String(apiFetchStub.mock.calls[0][0])
  expect(url).toContain("mime_type=image%2F")
  expect((document.querySelector('input[type="file"]') as HTMLInputElement).accept).not.toContain("audio")
})

it("does not filter by type for the body editor", async () => {
  render(<MediaPicker onClose={() => {}} onSelect={() => {}} />)
  await waitFor(() => expect(apiFetchStub).toHaveBeenCalled())
  expect(String(apiFetchStub.mock.calls[0][0])).not.toContain("mime_type")
})
```

- [ ] **Step 2: Run to see them fail**

Run: `npm test -- MediaPicker`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `MediaPickerProps` add:

```ts
  // Hide audio rows and refuse audio uploads. The featured-image and settings
  // pickers need an image; only the body editor takes audio.
  imagesOnly?: boolean
```

Destructure `imagesOnly = false` with the other props. In the listing request (line 104-108), after `if (request.search) params.set("search", request.search)`, add:

```ts
        if (imagesOnly) params.set("mime_type", "image/")
```

and add `imagesOnly` to that callback's dependency array. The name of the fetch stub in `MediaPicker.test.tsx` may differ from `apiFetchStub`; use whatever the file already mocks `useApiFetch` with.

Change the file input's `accept` to:

```tsx
            accept={imagesOnly ? "image/jpeg,image/png,image/gif,image/webp" : "image/jpeg,image/png,image/gif,image/webp,audio/mpeg,.mp3"}
```

Replace the `<img ... />` inside the tile (lines 282-293) with:

```tsx
                      {item.mime_type?.startsWith("audio/") ? (
                        <span aria-label="Audio file" className="flex h-full w-full items-center justify-center text-muted-foreground" role="img">
                          <Music className="h-10 w-10" />
                        </span>
                      ) : (
                        <img
                          alt={item.alt_text || item.file_name}
                          className="h-full w-full object-cover transition-transform duration-200 group-hover:scale-105"
                          loading="lazy"
                          referrerPolicy="no-referrer"
                          src={item.url}
                          srcSet={srcSetFor(item.variants)}
                          sizes="(min-width: 1024px) 20vw, (min-width: 768px) 25vw, (min-width: 640px) 33vw, 50vw"
                        />
                      )}
```

Add `Music` to the `lucide-react` import on line 2.

The alt-text edit control below the tile makes no sense for audio; wrap it in `{!item.mime_type?.startsWith("audio/") && (...)}`.

In `editArticleView.tsx:1845` and `settingsPage.tsx:854`, add `imagesOnly` to the `<MediaPicker` props. Confirm those two are the featured-image and settings pickers by reading five lines around each.

- [ ] **Step 4: Run**

Run: `npm test && npm run lint && npm run build`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add src/components/MediaPicker.tsx src/components/MediaPicker.test.tsx src/pages/editArticleView.tsx src/pages/settingsPage.tsx
git commit -m "feat(frontend): audio rows in the media picker, hidden where an image is required"
```

---

### Task 13: Pre-deploy checks, docs and PR text

**Files:**
- Modify: `README.md` (one paragraph under the media section, after line 125 where polls are described; keep it to the facts below)

- [ ] **Step 1: README paragraph**

Add under the media or editor section:

```
Audio and embeds. The editor's Embed button accepts YouTube, SoundCloud, Spotify, TikTok and Instagram links and stores them as shortcodes ([youtube URL], [spotify url="..." /] and so on) that the public site expands. MP3 files upload through the same media endpoint as images and are stored as [audio src="..." /]. The URL rules live in frontend/src/lib/embedUrl.ts and must stay identical to Scalene's copy; frontend/src/lib/embed-vectors.json is the shared test set.
```

- [ ] **Step 2: Run the whole suite one last time**

```bash
cd server && go test ./... && go vet ./... && cd ../frontend && npm test && npm run lint && npm run build
```

Expected: all green.

- [ ] **Step 3: Server-side checks, recorded in the PR, none of which change code here**

Run these against the deployed media origin and record the output. If any fails, the result goes to Sachin as a question before this branch deploys.

```bash
# 0. The CMS MEDIA_BASE_URL is set, uses https, and has the same origin as Scalene's MEDIA_BASE_URL.
# 1. Production media host for Scalene's build is not a legacy host.
#    (ask Sachin for the value of MEDIA_BASE_URL in the Scalene build env; it must not be cms.thetriangle.org or www.thetriangle.org)

# 2. Ranged GET through Cloudflare returns 206 with two bytes and the right type.
curl -s -r 0-1 -D - -o /dev/null https://<media host>/wp-content/uploads/<any existing file>.jpg | grep -iE 'HTTP/|content-range|content-type|x-content-type-options'
#    expect: HTTP/2 206, Content-Range: bytes 0-1/<size>, X-Content-Type-Options: nosniff

# 3. A real 60 MB MP3 upload through the admin app succeeds (watch the network tab for a 201 from /v1/media).

# 4. On the server: count MP3s a reindex would adopt.
find $MEDIA_ROOT/wp-content/uploads -iname '*.mp3' | wc -l

# 5. Effective CSP on www.thetriangle.org (read only).
curl -sI https://www.thetriangle.org/ | grep -i content-security-policy
```

- [ ] **Step 4: PR description**

```
Editor support for embeds and audio, the CMS half of the "audio player and media embeds" spec (docs/superpowers/specs/2026-10-06-article-embeds-design.md). Requires the Scalene PR to be live first; this branch may merge before it but must deploy after it.

Server
- /v1/media accepts audio/mpeg. Go's sniffer only knows ID3-tagged MP3s, so a bare MP3 named .mp3 is checked by its 4-byte frame header (rejects AAC, M4A, WAV). New package internal/mediatype shares the check with the indexer.
- /v1/media/fetch accepts MP3 URLs with a 5-minute timeout instead of 30 s.
- Reindex adopts on-disk .mp3 files only after the same header check.
- No new routes; swagger unchanged.

Frontend
- Embed button in the Trix toolbar. A pasted share URL is parsed client-side (frontend/src/lib/embedUrl.ts, byte-identical to Scalene's copy) and inserted as a static card; the player never runs in the admin app.
- trixImageHtml.ts renamed to trixArticleHtml.ts in its own commit. New branches convert cards and audio chips to shortcodes on save and back on load. Legacy [youtube]/[soundcloud] blocks open as cards and save back byte-for-byte when unedited; tested against every distinct block in the WordPress export (fixture committed with its extraction script).
- Stored card HTML is never rendered; cards are rebuilt from validated attributes on load and on paste.
- Save and autosave refuse while an upload is pending (Trix's isPending) or an embed is invalid, with a message naming the block. This also closes the existing gap where saving mid-image-upload stored a dead blob: figure.
- Media picker shows audio rows with a tile; the featured-image and settings pickers pass imagesOnly.

Not changed: nginx, Cloudflare, deploy scripts, the Scalene proxy, MEDIA_BASE_URL handling.

Pre-deploy checks: <paste results of Task 13 step 3>
```

- [ ] **Step 5: Hand to Eden**

Stop. Eden reviews the branch and decides on push and PR per his standing rule for these repos.
