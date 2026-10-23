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
// origin must therefore serve X-Content-Type-Options: nosniff (see docs/superpowers/specs/2026-10-06-article-embeds-design.md, section 6).
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
