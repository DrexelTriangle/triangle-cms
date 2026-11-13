package newsletter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func writeSlot(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSlotGate_Active(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "active")

	if ok, err := (SlotGate{}).Active(); !ok || err != nil {
		t.Fatalf("a lone backend must always send, got %v %v", ok, err)
	}

	if _, err := (SlotGate{Slot: "blue", File: file}).Active(); err == nil {
		t.Fatal("a missing marker must be an error, not a guess")
	}

	writeSlot(t, file, "green\n")
	if ok, err := (SlotGate{Slot: "green", File: file}).Active(); !ok || err != nil {
		t.Fatalf("green is live: got %v %v", ok, err)
	}
	if ok, err := (SlotGate{Slot: "blue", File: file}).Active(); ok || err != nil {
		t.Fatalf("blue is idle: got %v %v", ok, err)
	}

	writeSlot(t, file, "purple")
	if ok, err := (SlotGate{Slot: "blue", File: file}).Active(); ok || err == nil {
		t.Fatalf("a malformed marker must be an error, got %v %v", ok, err)
	}
}

// An idle or unsure slot must not touch the database at all: with no
// connection to hand, any query would panic.
func TestWorker_StandsByWhenNotLive(t *testing.T) {
	for name, active := range map[string]func() (bool, error){
		"idle":    func() (bool, error) { return false, nil },
		"unknown": func() (bool, error) { return false, errors.New("no marker") },
	} {
		t.Run(name, func(t *testing.T) {
			w := &Worker{Active: active, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			for range 2 {
				if err := w.Tick(context.Background()); err != nil {
					t.Fatalf("tick: %v", err)
				}
			}
		})
	}
}
