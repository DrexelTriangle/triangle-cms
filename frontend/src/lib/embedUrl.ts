/**
 * Share-URL validation and shortcode (de)serialization for article embeds.
 *
 * This file is copied byte-for-byte into the CMS editor
 * (triangle-cms/frontend/src/lib/embedUrl.ts) so that what the editor accepts
 * and what this site renders can never disagree. Both copies run the same
 * embed-vectors.json. Change one, change both.
 *
 * Every accepted URL is rebuilt from its validated parts (host plus captured
 * ID). Nothing a writer pasted reaches the page as-is, which is what keeps
 * tracking parameters and attribute-breaking characters out of the markup.
 */

export type EmbedProvider = "youtube" | "soundcloud" | "spotify" | "tiktok" | "instagram";

// media: for Instagram, the post's images already copied into our media
// library, as site-relative wp-content paths. Written by the editor after the
// import call, read back from the shortcode's media attribute.
export type Embed = { provider: EmbedProvider; url: string; media?: string[] };

/**
 * One media path: under the uploads prefix, no climbing, no query, an image
 * extension. The same rule Scalene applies before it renders the holder.
 */
export function isMediaPath(path: string): boolean {
  if (!path.startsWith("/wp-content/uploads/")) return false;
  if (path.includes("..") || path.includes("//") || /[?#\s]/.test(path)) return false;
  return /\.(webp|jpe?g|png)$/i.test(path);
}

/** A comma-separated media list; one bad path drops the whole list. */
export function parseMediaList(list: string): string[] {
  const paths = list.split(",").map((p) => p.trim()).filter(Boolean);
  return paths.length > 0 && paths.every(isMediaPath) ? paths : [];
}

export type ParseResult =
  | { ok: true; embed: Embed }
  | { ok: false; reason: "short-link" | "invalid" };

const INVALID: ParseResult = { ok: false, reason: "invalid" };
const SHORT_LINK: ParseResult = { ok: false, reason: "short-link" };

/** Hosts whose links need a redirect to resolve. We do no network calls, so
 *  these get a specific message asking for the full URL. */
const SHORT_LINK_HOSTS = new Set(["vm.tiktok.com", "vt.tiktok.com", "spotify.link", "on.soundcloud.com"]);

const YOUTUBE_HOSTS = new Set(["www.youtube.com", "youtube.com", "m.youtube.com"]);
const SOUNDCLOUD_HOSTS = new Set(["soundcloud.com", "www.soundcloud.com", "api.soundcloud.com"]);
const TIKTOK_HOSTS = new Set(["www.tiktok.com", "tiktok.com", "m.tiktok.com"]);
const INSTAGRAM_HOSTS = new Set(["www.instagram.com", "instagram.com"]);

const YOUTUBE_ID = /^[\w-]{11}$/;
const YOUTUBE_PATH_ID = /^\/(?:shorts|live)\/([\w-]{11})\/?$/;
const YOUTU_BE_PATH = /^\/([\w-]{11})\/?$/;
const SOUNDCLOUD_PATH = /^\/(?:[\w.-]+\/[\w.-]+|tracks\/\d+)\/?$/;
const SPOTIFY_INTL_PREFIX = /^\/intl-[a-z]{2}(?:-[a-z]{2})?(?=\/)/;
const SPOTIFY_PATH = /^\/(track|episode|show|album|playlist)\/([A-Za-z0-9]{22})\/?$/;
const TIKTOK_PATH = /^\/@([\w.-]+)\/video\/(\d+)\/?$/;
const INSTAGRAM_PATH = /^\/(?:[\w.]+\/)?(p|reel|reels)\/([A-Za-z0-9_-]+)\/?$/;

/** Parse a user-supplied string as an http(s) URL with no userinfo, and no port unless allowed. */
function parseUrl(input: string, { allowPort = false }: { allowPort?: boolean } = {}): URL | null {
  let url: URL;
  try {
    url = new URL(input.trim());
  } catch {
    return null;
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") return null;
  if (url.username || url.password || (url.port && !allowPort)) return null;
  return url;
}

function youtube(id: string): ParseResult {
  if (!YOUTUBE_ID.test(id)) return INVALID;
  return { ok: true, embed: { provider: "youtube", url: `https://www.youtube.com/watch?v=${id}` } };
}

export function parseEmbedUrl(input: string): ParseResult {
  const url = parseUrl(input);
  if (!url) return INVALID;
  const host = url.hostname.toLowerCase();

  if (SHORT_LINK_HOSTS.has(host)) return SHORT_LINK;

  if (YOUTUBE_HOSTS.has(host)) {
    if (url.pathname === "/watch") return youtube(url.searchParams.get("v") ?? "");
    const m = YOUTUBE_PATH_ID.exec(url.pathname);
    return youtube(m ? m[1] : "");
  }
  if (host === "youtu.be") {
    const m = YOUTU_BE_PATH.exec(url.pathname);
    return youtube(m ? m[1] : "");
  }
  if (SOUNDCLOUD_HOSTS.has(host)) {
    if (!SOUNDCLOUD_PATH.test(url.pathname)) return INVALID;
    // SoundCloud's ID is not in the share URL, so the path is kept. The query
    // is dropped except secret_token, which private tracks need to play.
    const out = new URL(`https://${host}${url.pathname.replace(/\/$/, "")}`);
    const token = url.searchParams.get("secret_token");
    if (token && /^[\w-]+$/.test(token)) out.searchParams.set("secret_token", token);
    return { ok: true, embed: { provider: "soundcloud", url: out.toString() } };
  }
  if (host === "open.spotify.com") {
    const m = SPOTIFY_PATH.exec(url.pathname.replace(SPOTIFY_INTL_PREFIX, ""));
    if (!m) return INVALID;
    return { ok: true, embed: { provider: "spotify", url: `https://open.spotify.com/${m[1]}/${m[2]}` } };
  }
  if (TIKTOK_HOSTS.has(host)) {
    const m = TIKTOK_PATH.exec(url.pathname);
    if (!m) return INVALID;
    return { ok: true, embed: { provider: "tiktok", url: `https://www.tiktok.com/@${m[1]}/video/${m[2]}` } };
  }
  if (INSTAGRAM_HOSTS.has(host)) {
    const m = INSTAGRAM_PATH.exec(url.pathname);
    if (!m) return INVALID;
    const kind = m[1] === "reels" ? "reel" : m[1];
    return { ok: true, embed: { provider: "instagram", url: `https://www.instagram.com/${kind}/${m[2]}/` } };
  }
  return INVALID;
}

/**
 * Validate an uploaded-audio URL: https, exactly the media origin, under the
 * WordPress uploads prefix, an .mp3 path. Returns the URL with query and
 * fragment removed, or null. An empty mediaBaseUrl rejects everything, which
 * is what a build without MEDIA_BASE_URL should do.
 */
export function parseAudioUrl(input: string, mediaBaseUrl: string): string | null {
  // A port is fine here because the origin check below compares it too.
  const url = parseUrl(input, { allowPort: true });
  if (!url || url.protocol !== "https:") return null;
  let origin: string;
  try {
    origin = new URL(mediaBaseUrl).origin;
  } catch {
    return null;
  }
  if (url.origin !== origin) return null;
  if (!url.pathname.startsWith("/wp-content/uploads/")) return null;
  if (!/\.mp3$/i.test(url.pathname)) return null;
  return `${url.origin}${url.pathname}`;
}

/**
 * The shortcode attribute grammar: inside url="..." only these characters
 * appear. Everything else is percent-encoded, so a quote or a closing bracket
 * can never end the attribute or the shortcode early.
 */
const SHORTCODE_SAFE_CHAR = /^[A-Za-z0-9\-._~:/?#@!$&'()*+,;=%]$/;

export function encodeShortcodeUrl(url: string): string {
  let out = "";
  for (const ch of url) {
    out += SHORTCODE_SAFE_CHAR.test(ch) ? ch : encodeURIComponent(ch);
  }
  return out;
}

/** One template per provider; YouTube keeps its legacy bare form. */
export function embedToShortcode(embed: Embed): string {
  if (embed.provider === "youtube") return `[youtube ${embed.url}]`;
  const media = embed.provider === "instagram" && embed.media?.length ? ` media="${embed.media.join(",")}"` : "";
  return `[${embed.provider} url="${encodeShortcodeUrl(embed.url)}"${media} /]`;
}

const YOUTUBE_SHORTCODE = /^\[youtube\s+(\S+?)\s*\/?\]$/i;
const ATTR_SHORTCODE = /^\[(soundcloud|spotify|tiktok|instagram)(?=\s)([^\]]*?)\s*\/?\]$/i;
const URL_ATTR = /(?:^|\s)url\s*=\s*(?:"([^"]*)"|'([^']*)')/i;
const MEDIA_ATTR = /(?:^|\s)media\s*=\s*(?:"([^"]*)"|'([^']*)')/i;

/**
 * Parse text that is exactly one shortcode. Legacy attribute-form shortcodes
 * with extra attributes parse too, as long as their url validates. Returns the
 * canonical embed, or null when the text is not one valid shortcode.
 */
export function shortcodeToEmbed(text: string): Embed | null {
  const t = text.trim();
  const yt = YOUTUBE_SHORTCODE.exec(t);
  if (yt) {
    const r = parseEmbedUrl(yt[1]);
    return r.ok && r.embed.provider === "youtube" ? r.embed : null;
  }
  const m = ATTR_SHORTCODE.exec(t);
  if (!m) return null;
  const attr = URL_ATTR.exec(m[2]);
  if (!attr) return null;
  const r = parseEmbedUrl(attr[1] ?? attr[2] ?? "");
  if (!r.ok || r.embed.provider !== m[1].toLowerCase()) return null;
  if (r.embed.provider === "instagram") {
    const mediaAttr = MEDIA_ATTR.exec(m[2]);
    const media = parseMediaList(mediaAttr?.[1] ?? mediaAttr?.[2] ?? "");
    if (media.length > 0) return { ...r.embed, media };
  }
  return r.embed;
}

/** Longest "where this audio is from" line the shortcode carries. */
export const AUDIO_SOURCE_MAX = 120;

/**
 * Free text inside a shortcode attribute uses the URL grammar above: a quote,
 * a bracket or a space is percent-encoded, so the writer's text can never
 * close the attribute early. decodeShortcodeText reverses it.
 */
export function decodeShortcodeText(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function audioToShortcode(src: string, source = ""): string {
  const text = source.trim().slice(0, AUDIO_SOURCE_MAX);
  const sourceAttr = text ? ` source="${encodeShortcodeUrl(text)}"` : "";
  return `[audio src="${encodeShortcodeUrl(src)}"${sourceAttr} /]`;
}

const AUDIO_SHORTCODE = /^\[audio(?=\s)([^\]]*?)\s*\/?\]$/i;
const SRC_ATTR = /(?:^|\s)src\s*=\s*(?:"([^"]*)"|'([^']*)')/i;
const SOURCE_ATTR = /(?:^|\s)source\s*=\s*(?:"([^"]*)"|'([^']*)')/i;

export type AudioShortcode = { src: string; source: string };

export function parseAudioShortcode(text: string, mediaBaseUrl: string): AudioShortcode | null {
  const m = AUDIO_SHORTCODE.exec(text.trim());
  if (!m) return null;
  const attr = SRC_ATTR.exec(m[1]);
  if (!attr) return null;
  const src = parseAudioUrl(attr[1] ?? attr[2] ?? "", mediaBaseUrl);
  if (!src) return null;
  const sourceAttr = SOURCE_ATTR.exec(m[1]);
  const source = decodeShortcodeText(sourceAttr?.[1] ?? sourceAttr?.[2] ?? "").trim().slice(0, AUDIO_SOURCE_MAX);
  return { src, source };
}

export function shortcodeToAudio(text: string, mediaBaseUrl: string): string | null {
  return parseAudioShortcode(text, mediaBaseUrl)?.src ?? null;
}

/** The quiz shortcode carries only a CMS id; the site fetches the rest. */
export function quizToShortcode(id: number): string {
  return `[quiz id="${id}"]`;
}

// Whitespace is spelled out because JS \s also matches U+00A0 and Go's does not.
const QUIZ_SHORTCODE = /^\[quiz[ \t\n\f\r]+id="(\d+)"[ \t\n\f\r]*\/?\]$/i;

export function shortcodeToQuizId(text: string): number | null {
  const m = QUIZ_SHORTCODE.exec(text.trim());
  if (!m) return null;
  const id = Number(m[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}
