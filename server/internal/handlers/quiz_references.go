package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

// quizShortcodeBody is the shape QUIZ_SHORTCODE in frontend/src/lib/embedUrl.ts
// accepts: the name in any case, a digits-only id, optional space and slash
// before the closing bracket. Keep the two in step; TestQuizShortcodeIDMatchesTheSharedVectors
// checks them against embed-vectors.json.
const quizShortcodeBody = `\[quiz[ \t\n\f\r]+id="(\d+)"[ \t\n\f\r]*/?\]`

var (
	quizShortcodeInText = regexp.MustCompile(`(?i)` + quizShortcodeBody)
	quizShortcodeWhole  = regexp.MustCompile(`(?i)^` + quizShortcodeBody + `$`)
)

// maxQuizID is Number.MAX_SAFE_INTEGER, the largest id JavaScript reads exactly.
const maxQuizID = 1<<53 - 1

// quizShortcodeID mirrors shortcodeToQuizId: the trimmed text must be one quiz
// shortcode whose id is 1 or more.
func quizShortcodeID(text string) (int64, bool) {
	m := quizShortcodeWhole.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 || id > maxQuizID {
		return 0, false
	}
	return id, true
}

// quizIDsInContent finds quiz shortcodes anywhere in the body, not only in a
// block of their own, so the gate refuses rather than misses.
func quizIDsInContent(html string) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	for _, match := range quizShortcodeInText.FindAllString(html, -1) {
		id, ok := quizShortcodeID(match)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// rejectUnpublishedQuizzes is the server's half of the editor rule: an article
// may be saved as a draft with any quiz card, but may not go live pointing at
// a quiz the site would 404. Returns true when a 422 was written.
func rejectUnpublishedQuizzes(w http.ResponseWriter, r *http.Request, conn *sql.DB, content string) bool {
	ids := quizIDsInContent(content)
	if len(ids) == 0 {
		return false
	}
	found, err := db.GetPublishedQuizzesByIDs(r.Context(), conn, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to check quizzes")
		return true
	}
	for _, id := range ids {
		if found[id] == nil {
			writeJSON(w, http.StatusUnprocessableEntity, models.ErrorResponse{Error: fmt.Sprintf("Quiz #%d is not published. Publish it or remove it from the article.", id)})
			return true
		}
	}
	return false
}
