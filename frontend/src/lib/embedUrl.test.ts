import { describe, expect, it } from "vitest";

import vectors from "./embed-vectors.json";
import {
  audioToShortcode,
  parseAudioShortcode,
  parseMediaList,
  embedToShortcode,
  encodeShortcodeUrl,
  parseAudioUrl,
  parseEmbedUrl,
  shortcodeToAudio,
  shortcodeToEmbed,
} from "./embedUrl";

describe("parseEmbedUrl", () => {
  for (const v of vectors.accept) {
    it(`accepts ${v.input}`, () => {
      expect(parseEmbedUrl(v.input)).toEqual({ ok: true, embed: { provider: v.provider, url: v.url } });
    });
  }
  for (const v of vectors.reject) {
    it(`rejects ${v.input || "(empty)"} as ${v.reason}`, () => {
      expect(parseEmbedUrl(v.input)).toEqual({ ok: false, reason: v.reason });
    });
  }
});

describe("parseAudioUrl", () => {
  const base = vectors.audio.mediaBaseUrl;
  for (const v of vectors.audio.accept) {
    it(`accepts ${v.input}`, () => {
      expect(parseAudioUrl(v.input, base)).toBe(v.url);
    });
  }
  for (const input of vectors.audio.reject) {
    it(`rejects ${input || "(empty)"}`, () => {
      expect(parseAudioUrl(input, base)).toBeNull();
    });
  }
  it("rejects everything when the media base URL is unset", () => {
    expect(parseAudioUrl(vectors.audio.accept[0].input, "")).toBeNull();
  });
  const ported = "https://media.example.test:8443/wp-content/uploads/2026/10/ep1.mp3";
  it("accepts a port when the media base URL has the same port", () => {
    expect(parseAudioUrl(ported, "https://media.example.test:8443")).toBe(ported);
  });
  it("rejects a port when the media base URL has no port", () => {
    expect(parseAudioUrl(ported, "https://media.example.test")).toBeNull();
  });
  it("still rejects userinfo", () => {
    expect(parseAudioUrl("https://u:p@media.example.test:8443/wp-content/uploads/2026/10/ep1.mp3", "https://media.example.test:8443")).toBeNull();
  });
});

describe("shortcode round trip", () => {
  for (const v of vectors.shortcodes) {
    it(`parses and re-emits ${v.text}`, () => {
      const embed = shortcodeToEmbed(v.text);
      expect(embed).toEqual({ provider: v.provider, url: v.url });
      expect(embedToShortcode(embed!)).toBe(v.text);
    });
  }
  for (const text of vectors.shortcodeRejects) {
    it(`does not parse ${text}`, () => {
      expect(shortcodeToEmbed(text)).toBeNull();
    });
  }
  it("round-trips audio", () => {
    const base = vectors.audio.mediaBaseUrl;
    const src = vectors.audio.accept[0].url;
    const text = audioToShortcode(src);
    expect(text).toBe(`[audio src="${src}" /]`);
    expect(shortcodeToAudio(text, base)).toBe(src);
  });
  it("tolerates surrounding whitespace", () => {
    expect(shortcodeToEmbed("  [youtube https://youtu.be/dQw4w9WgXcQ]\n")).toEqual({
      provider: "youtube",
      url: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    });
  });
});

describe("instagram media", () => {
  const url = "https://www.instagram.com/p/Cabc123_-x/";
  const one = "/wp-content/uploads/2026/10/a.jpg";
  const two = "/wp-content/uploads/2026/10/b.webp";
  it("writes the media list onto the shortcode and reads it back", () => {
    const text = embedToShortcode({ provider: "instagram", url, media: [one, two] });
    expect(text).toBe(`[instagram url="${url}" media="${one},${two}" /]`);
    expect(shortcodeToEmbed(text)).toEqual({ provider: "instagram", url, media: [one, two] });
  });
  it("drops a media list with a path off the uploads tree", () => {
    expect(shortcodeToEmbed(`[instagram url="${url}" media="${one},https://evil.test/x.jpg" /]`)).toEqual({ provider: "instagram", url });
  });
  it("ignores media on other providers", () => {
    expect(embedToShortcode({ provider: "tiktok", url: "https://www.tiktok.com/@a/video/123", media: [one] })).toBe(
      `[tiktok url="https://www.tiktok.com/@a/video/123" /]`,
    );
  });
  it("validates one path at a time", () => {
    expect(parseMediaList(` ${one} , ${two}`)).toEqual([one, two]);
    expect(parseMediaList("/wp-content/uploads/../x.png")).toEqual([]);
    expect(parseMediaList("/wp-content/uploads/x.svg")).toEqual([]);
    expect(parseMediaList("")).toEqual([]);
  });
});

describe("audio source", () => {
  const base = "https://media.test";
  const src = "https://media.test/wp-content/uploads/2026/10/ep1.mp3";
  it("writes and reads the source line, quotes and brackets included", () => {
    const text = audioToShortcode(src, ` WKDU "91.7" [FM] `);
    expect(text).toBe(`[audio src="${src}" source="WKDU%20%2291.7%22%20%5BFM%5D" /]`);
    expect(parseAudioShortcode(text, base)).toEqual({ src, source: 'WKDU "91.7" [FM]' });
  });
  it("omits the attribute for a blank source and reads it back empty", () => {
    expect(audioToShortcode(src, "  ")).toBe(`[audio src="${src}" /]`);
    expect(parseAudioShortcode(`[audio src="${src}" /]`, base)).toEqual({ src, source: "" });
  });
  it("cuts the source to 120 characters", () => {
    expect(audioToShortcode(src, "y".repeat(130))).toBe(`[audio src="${src}" source="${"y".repeat(120)}" /]`);
  });
});

describe("encodeShortcodeUrl", () => {
  it("percent-encodes the characters that would break a shortcode attribute", () => {
    expect(encodeShortcodeUrl('https://a.test/x"y]z w<>')).toBe("https://a.test/x%22y%5Dz%20w%3C%3E");
  });
  it("leaves an already safe URL alone", () => {
    const url = "https://soundcloud.com/a/b?secret_token=s-1&x=%20";
    expect(encodeShortcodeUrl(url)).toBe(url);
  });
});
