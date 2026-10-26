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
