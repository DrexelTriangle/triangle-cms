package newsletter

import (
	"fmt"
	"os"
	"strings"
)

// DefaultActiveSlotFile is where the deploy scripts' marker naming the live
// slot is mounted in a production backend (see deploy/compose.cms.yml).
const DefaultActiveSlotFile = "/run/triangle-cms-slot/active"

// SlotGate keeps the sender to the slot Nginx is serving. Production runs two
// backend slots, and the idle one keeps the previous release until the next
// deploy replaces it: left to send, it mailed whole issues with code the live
// release had already moved past (on 2026-11-07, without per-link tracking).
//
// Slot is this process's own slot; File holds the live one, written by
// deploy.sh and rollback.sh whenever Nginx switches. A zero SlotGate (no Slot)
// is a single backend, which is always the one to send.
type SlotGate struct {
	Slot string
	File string
}

// ValidSlot reports whether s names a production slot.
func ValidSlot(s string) bool { return s == "blue" || s == "green" }

// Active reports whether this process's slot is the live one. An unreadable
// or malformed marker is an error, and the caller must then not send: two
// slots each guessing they are live is the failure this exists to prevent.
func (g SlotGate) Active() (bool, error) {
	if g.Slot == "" {
		return true, nil
	}
	file := g.File
	if file == "" {
		file = DefaultActiveSlotFile
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return false, fmt.Errorf("read the active slot: %w", err)
	}
	live := strings.TrimSpace(string(raw))
	if !ValidSlot(live) {
		return false, fmt.Errorf("active slot file %s names %q, not blue or green", file, live)
	}
	return live == g.Slot, nil
}
