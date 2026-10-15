package database

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBytes(t *testing.T, dir, name string, b []byte) string {
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

	if !isAdoptableAudio(writeBytes(t, dir, "a.mp3", id3)) {
		t.Error("ID3-tagged file should be adoptable")
	}
	if !isAdoptableAudio(writeBytes(t, dir, "b.mp3", frame)) {
		t.Error("bare-frame file should be adoptable")
	}
	if isAdoptableAudio(writeBytes(t, dir, "c.mp3", text)) {
		t.Error("text named .mp3 should not be adoptable")
	}
	if isAdoptableAudio(filepath.Join(dir, "missing.mp3")) {
		t.Error("missing file should not be adoptable")
	}
}
