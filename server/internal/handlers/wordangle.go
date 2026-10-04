package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"server/internal/wordangle"
)

// How far ahead an editor may schedule. Generous enough for a month batch
// queued well in advance; the cap only stops a typo'd year.
const wordangleMaxDaysAhead = 400

// Generate windows, counted from today inclusive. "month" is a calendar month
// ahead, so generating on Oct 4 fills through Nov 3.
var wordangleGenerateSpans = map[string]func(time.Time) time.Time{
	"day":   func(today time.Time) time.Time { return today },
	"week":  func(today time.Time) time.Time { return today.AddDate(0, 0, 6) },
	"month": func(today time.Time) time.Time { return today.AddDate(0, 1, -1) },
}

type wordangleManageResponse struct {
	Today string                 `json:"today"`
	Queue []models.WordangleWord `json:"queue"`
	Seen  []models.WordangleWord `json:"seen"`
}

type wordangleGenerateRequest struct {
	Span string `json:"span"`
}

type wordangleSetRequest struct {
	// Empty means regenerate: draw a random unused word from the pool.
	Word string `json:"word"`
}

type wordangleCheckResponse struct {
	Word   string                `json:"word"`
	IsWord bool                  `json:"is_word"`
	Status string                `json:"status"` // "unused", "queued" or "seen"
	Entry  *models.WordangleWord `json:"entry,omitempty"`
}

// @Summary Get a day's Wordangle answer
// @Description Public. Only today and past days are served; a future day reads as 404 so the queue cannot be spoiled. An empty today is filled with a generated word on first read, so every played word lands on the seen-before list.
// @Tags wordangle
// @Produce json
// @Param date path string true "Puzzle date (YYYY-MM-DD)"
// @Success 200 {object} models.WordangleWord
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Router /v1/wordangle/days/{date} [get]
func GetWordangleDay(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		date, err := wordangle.ParseDate(r.PathValue("date"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid date")
			return
		}
		today := wordangle.Today(time.Now())
		if date.After(today) {
			writeError(w, http.StatusNotFound, "no word for that day")
			return
		}
		var entry models.WordangleWord
		if date.Equal(today) {
			entry, err = db.EnsureTodaysWordangleWord(r.Context(), conn)
		} else {
			entry, err = db.GetWordangleWordByDate(r.Context(), conn, date)
		}
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrWordanglePoolExhausted) {
			writeError(w, http.StatusNotFound, "no word for that day")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load word")
			return
		}
		w.Header().Set("Cache-Control", publicReadCacheControl)
		writeJSON(w, http.StatusOK, models.WordangleWord{Date: entry.Date, Number: entry.Number, Word: entry.Word})
	})
}

// wordangleListCacheControl: the lists change only when the CMS is redeployed.
const wordangleListCacheControl = "public, max-age=3600, stale-while-revalidate=86400"

// @Summary Get a Wordangle word list
// @Description Public. targets.txt (answer pool), allowed-2.txt through allowed-6.txt (guess dictionaries) and SCOWL-Copyright.txt, one lowercase word per line.
// @Tags wordangle
// @Produce plain
// @Param file path string true "List file name"
// @Success 200 {string} string
// @Failure 404 {object} models.ErrorResponse
// @Router /v1/wordangle/lists/{file} [get]
func GetWordangleList() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := wordangle.ListFile(r.PathValue("file"))
		if !ok {
			writeError(w, http.StatusNotFound, "no such list")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", wordangleListCacheControl)
		w.Write(body)
	})
}

// @Summary Wordangle queue and seen-before list
// @Tags wordangle
// @Produce json
// @Success 200 {object} wordangleManageResponse
// @Security BearerAuth
// @Router /v1/wordangle/manage [get]
func GetWordangleManage(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeWordangleManage(w, r, conn, http.StatusOK)
	})
}

// @Summary Fill empty Wordangle days with generated words
// @Tags wordangle
// @Accept json
// @Produce json
// @Param body body wordangleGenerateRequest true "span: day, week or month"
// @Success 200 {object} wordangleManageResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/wordangle/generate [post]
func PostWordangleGenerate(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body wordangleGenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		end, ok := wordangleGenerateSpans[body.Span]
		if !ok {
			writeError(w, http.StatusBadRequest, "span must be day, week or month")
			return
		}
		today := wordangle.Today(time.Now())
		filled, err := db.GenerateWordangleWords(r.Context(), conn, today, end(today), wordangleActor(r))
		if filled > 0 {
			activity.LogRequest(r, "wordangle_updated", fmt.Sprintf("Generated %d Wordangle word(s)", filled))
		}
		if errors.Is(err, db.ErrWordanglePoolExhausted) {
			writeError(w, http.StatusConflict, fmt.Sprintf("Ran out of unused words after filling %d day(s); write the rest in by hand", filled))
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to generate words")
			return
		}
		writeWordangleManage(w, r, conn, http.StatusOK)
	})
}

// @Summary Set or regenerate one day's Wordangle word
// @Description An empty word draws a new random one. Past days cannot change, and a word that is queued or on the seen-before list is refused.
// @Tags wordangle
// @Accept json
// @Produce json
// @Param date path string true "Puzzle date (YYYY-MM-DD)"
// @Param body body wordangleSetRequest true "word, or empty to regenerate"
// @Success 200 {object} models.WordangleWord
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/wordangle/days/{date} [put]
func PutWordangleDay(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		date, err := wordangle.ParseDate(r.PathValue("date"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid date")
			return
		}
		today := wordangle.Today(time.Now())
		if date.Before(today) {
			writeError(w, http.StatusConflict, "Past puzzles can't be changed")
			return
		}
		if date.After(today.AddDate(0, 0, wordangleMaxDaysAhead)) {
			writeError(w, http.StatusBadRequest, "That date is too far ahead")
			return
		}
		var body wordangleSetRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		word := wordangle.Normalize(body.Word)
		var entry models.WordangleWord
		if word == "" {
			entry, err = db.RegenerateWordangleWord(r.Context(), conn, date, wordangleActor(r))
		} else {
			if !wordangle.IsWord(word) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("%q isn't a six-letter word in the dictionary", word))
				return
			}
			entry, err = db.SetWordangleWord(r.Context(), conn, date, word, models.WordangleSourceCustom, wordangleActor(r))
		}
		switch {
		case errors.Is(err, db.ErrWordangleWordTaken):
			writeError(w, http.StatusConflict, wordangleTakenMessage(r, conn, word))
			return
		case errors.Is(err, db.ErrWordanglePastDay):
			writeError(w, http.StatusConflict, "Past puzzles can't be changed")
			return
		case errors.Is(err, db.ErrWordanglePoolExhausted):
			writeError(w, http.StatusConflict, "Every generated word has been used; write one in by hand")
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, "Failed to set word")
			return
		}
		activity.LogRequest(r, "wordangle_updated", fmt.Sprintf("Wordangle %s set (%s)", entry.Date, entry.Source))
		writeJSON(w, http.StatusOK, entry)
	})
}

// @Summary Check a word against the Wordangle dictionary and seen-before list
// @Tags wordangle
// @Produce json
// @Param word query string true "Word to check"
// @Success 200 {object} wordangleCheckResponse
// @Security BearerAuth
// @Router /v1/wordangle/check [get]
func GetWordangleCheck(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		word := wordangle.Normalize(r.URL.Query().Get("word"))
		resp := wordangleCheckResponse{Word: word, IsWord: wordangle.IsWord(word), Status: "unused"}
		entry, err := db.LookupWordangleWord(r.Context(), conn, word)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			writeError(w, http.StatusInternalServerError, "Failed to check word")
			return
		default:
			resp.Entry = &entry
			resp.Status = wordangleStatus(entry, wordangle.Today(time.Now()))
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

func writeWordangleManage(w http.ResponseWriter, r *http.Request, conn *sql.DB, status int) {
	words, err := db.ListWordangleWords(r.Context(), conn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load Wordangle words")
		return
	}
	today := wordangle.Today(time.Now())
	resp := wordangleManageResponse{
		Today: today.Format(time.DateOnly),
		Queue: []models.WordangleWord{},
		Seen:  []models.WordangleWord{},
	}
	for _, word := range words {
		if wordangleStatus(word, today) == "queued" {
			resp.Queue = append(resp.Queue, word)
		} else {
			resp.Seen = append(resp.Seen, word)
		}
	}
	// Newest first: the seen list is read to answer "did we just use this?".
	for i, j := 0, len(resp.Seen)-1; i < j; i, j = i+1, j-1 {
		resp.Seen[i], resp.Seen[j] = resp.Seen[j], resp.Seen[i]
	}
	writeJSON(w, status, resp)
}

// wordangleStatus places a row: today's and future words are still queued
// (today's can be swapped out), anything earlier or retired has been seen.
func wordangleStatus(entry models.WordangleWord, today time.Time) string {
	if entry.Date == "" || entry.Date < today.Format(time.DateOnly) {
		return "seen"
	}
	return "queued"
}

func wordangleTakenMessage(r *http.Request, conn *sql.DB, word string) string {
	entry, err := db.LookupWordangleWord(r.Context(), conn, word)
	if err != nil || word == "" {
		return "That word has already been used"
	}
	switch {
	case entry.RetiredOn != "":
		return fmt.Sprintf("%q was already live on %s", word, entry.RetiredOn)
	case wordangleStatus(entry, wordangle.Today(time.Now())) == "queued":
		return fmt.Sprintf("%q is already queued for %s", word, entry.Date)
	default:
		return fmt.Sprintf("%q was already used on %s", word, entry.Date)
	}
}

func wordangleActor(r *http.Request) string {
	if user, ok := middleware.UserFromContext(r.Context()); ok && user != nil {
		return user.Name
	}
	return ""
}
