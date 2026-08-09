package database

import (
	"strings"
	"testing"
)

// Every handler that reads RowsAffected treats zero as "no such row" and answers
// 404. Without clientFoundRows an UPDATE that matches its row but changes none of
// its values reports zero, so a save that happened to change nothing is answered
// as though the article had been deleted mid-edit.
func TestBuildDSN_RequestsFoundRowsSemantics(t *testing.T) {
	dsn := buildDSN("cms", "user", "pw", "db1.internal", 3306)
	if !strings.Contains(dsn, "clientFoundRows=true") {
		t.Errorf("DSN = %q, want clientFoundRows=true", dsn)
	}
	// parseTime drives every DATETIME column into time.Time; losing it would turn
	// every date in the API into a []byte.
	if !strings.Contains(dsn, "parseTime=true") {
		t.Errorf("DSN = %q, want parseTime=true kept", dsn)
	}
}
