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

// jsTrimSpace is what JavaScript's String.prototype.trim strips, which is what
// Scalene trims a block's content with before matching the shortcode.
const jsTrimSpace = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*`

// quizShortcodeBlock matches a <p> or <div> whose whole content is one quiz
// shortcode, the only place Scalene (expandBlockShortcode in shortcodes.ts)
// renders a quiz. The tag syntax follows Scalene's TAG pattern in articleHtml.ts.
// It is deliberately a little wider than Scalene, so the gate can refuse a
// block the site would not render but never miss one it would: any depth
// rather than top level only, and any closing tag, which is all Scalene's
// depth count checks.
var quizShortcodeBlock = regexp.MustCompile(`(?i)<(?:p|div)(?:(?:"[^"]*"|'[^']*'|[^\w'">-])(?:"[^"]*"|'[^']*'|[^'">])*)?>` +
	jsTrimSpace + `(` + quizShortcodeBody + `)` + jsTrimSpace +
	`</[a-z][\w-]*(?:"[^"]*"|'[^']*'|[^'">])*>`)

var quizShortcodeWhole = regexp.MustCompile(`(?i)^` + quizShortcodeBody + `$`)

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

// quizIDsInContent finds the quiz shortcodes the site would render. A
// shortcode quoted in a sentence or a code sample stays text on the site, so
// it does not hold up publishing.
func quizIDsInContent(html string) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	for _, match := range quizShortcodeBlock.FindAllStringSubmatch(html, -1) {
		id, ok := quizShortcodeID(match[1])
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
