package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"server/internal/activity"
	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
	"server/internal/trimeline"
)

// Generate windows, counted from today inclusive, mirroring Wordangle.
var trimelineGenerateSpans = map[string]func(time.Time) time.Time{
	"day":   func(today time.Time) time.Time { return today },
	"week":  func(today time.Time) time.Time { return today.AddDate(0, 0, 6) },
	"month": func(today time.Time) time.Time { return today.AddDate(0, 1, -1) },
}

type trimelineManageResponse struct {
	Today string                   `json:"today"`
	Queue []models.TrimelinePuzzle `json:"queue"`
	Past  []models.TrimelinePuzzle `json:"past"`
}

type trimelineGenerateRequest struct {
	Span string `json:"span"`
}

// @Summary Get a day's Tri-meline puzzle
// @Description Public. Only days from launch (#0, 2026-11-10) through today are served; a future day reads as 404 so the queue cannot be spoiled, and a day before launch reads as 404 because no public puzzle exists for it. An empty today is drawn on first read. The five headlines are returned in randomized order with each article's pub_date (RFC3339), following Wordangle's decision that shipping the answer to the client is acceptable; the client-side check is against a shuffled list, so view-source does not hand over the sorted answer.
// @Tags trimeline
// @Produce json
// @Param date path string true "Puzzle date (YYYY-MM-DD)"
// @Success 200 {object} models.TrimelinePuzzle
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Router /v1/trimeline/days/{date} [get]
func GetTrimelineDay(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		date, err := trimeline.ParseDate(r.PathValue("date"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid date")
			return
		}
		// Pre-launch days never had a public puzzle, and future days must
		// not leak the queue.
		today := trimeline.Today(time.Now())
		if date.After(today) || trimeline.PuzzleNumber(date) < 0 {
			writeError(w, http.StatusNotFound, "no puzzle for that day")
			return
		}
		var puzzle models.TrimelinePuzzle
		if date.Equal(today) {
			puzzle, err = db.EnsureTodaysTrimelinePuzzle(r.Context(), conn)
		} else {
			puzzle, err = db.GetTrimelinePuzzleByDate(r.Context(), conn, date)
		}
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrTrimelineNoPuzzle) {
			writeError(w, http.StatusNotFound, "no puzzle for that day")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to load puzzle")
			return
		}
		// Shuffle the response so the picks aren't served pre-sorted; the
		// stored order stays chronological for the archive.
		shuffled := trimeline.Shuffle(headlineSlice(puzzle.Headlines))
		puzzle.Headlines = modelHeadlines(shuffled)
		// The public response carries only what a reader needs; internal
		// fields (who set it, when it was written) stay in the dashboard.
		puzzle.Source = ""
		puzzle.SetBy = ""
		puzzle.UpdatedAt = time.Time{}
		w.Header().Set("Cache-Control", publicReadCacheControl)
		writeJSON(w, http.StatusOK, puzzle)
	})
}

// @Summary Tri-meline queue and past puzzles
// @Tags trimeline
// @Produce json
// @Success 200 {object} trimelineManageResponse
// @Security BearerAuth
// @Router /v1/trimeline/manage [get]
func GetTrimelineManage(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTrimelineManage(w, r, conn, http.StatusOK)
	})
}

// @Summary Fill empty Tri-meline days with generated puzzles
// @Description For each empty day in the span, draws five archive headlines as a puzzle. Days that already have a puzzle are left alone; past puzzles never change.
// @Tags trimeline
// @Accept json
// @Produce json
// @Param body body trimelineGenerateRequest true "span: day, week or month"
// @Success 200 {object} trimelineManageResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/trimeline/generate [post]
func PostTrimelineGenerate(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body trimelineGenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		end, ok := trimelineGenerateSpans[body.Span]
		if !ok {
			writeError(w, http.StatusBadRequest, "span must be day, week or month")
			return
		}
		today := trimeline.Today(time.Now())
		filled, err := db.GenerateTrimelinePuzzles(r.Context(), conn, today, end(today), trimelineActor(r))
		if filled > 0 {
			activity.LogRequest(r, "trimeline_updated", fmt.Sprintf("Generated %d Tri-meline puzzle(s)", filled))
		}
		if errors.Is(err, db.ErrTrimelineNoPuzzle) {
			writeError(w, http.StatusConflict, fmt.Sprintf("Could not draw a valid puzzle after filling %d day(s); the archive pool is too thin or too clustered", filled))
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to generate puzzles")
			return
		}
		writeTrimelineManage(w, r, conn, http.StatusOK)
	})
}

func writeTrimelineManage(w http.ResponseWriter, r *http.Request, conn *sql.DB, status int) {
	puzzles, err := db.ListTrimelinePuzzles(r.Context(), conn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load Tri-meline puzzles")
		return
	}
	today := trimeline.Today(time.Now())
	resp := trimelineManageResponse{
		Today: today.Format(time.DateOnly),
		Queue: []models.TrimelinePuzzle{},
		Past:  []models.TrimelinePuzzle{},
	}
	for _, puzzle := range puzzles {
		if puzzle.Date >= today.Format(time.DateOnly) {
			resp.Queue = append(resp.Queue, puzzle)
		} else {
			// Newest first, like the Wordangle seen list.
			resp.Past = append([]models.TrimelinePuzzle{puzzle}, resp.Past...)
		}
	}
	writeJSON(w, status, resp)
}

func headlineSlice(headlines []models.TrimelineHeadline) []trimeline.Headline {
	out := make([]trimeline.Headline, 0, len(headlines))
	for _, h := range headlines {
		pubDate, err := time.Parse(time.RFC3339, h.PubDate)
		if err != nil {
			// A corrupt stored pub_date must not ship to readers as year 1;
			// skip the headline so a broken row surfaces as a short puzzle
			// rather than a nonsense one.
			slog.Warn("trimeline headline has unparseable pub_date", "slug", h.Slug, "pub_date", h.PubDate, "error", err)
			continue
		}
		out = append(out, trimeline.Headline{Slug: h.Slug, Title: h.Title, PubDate: pubDate})
	}
	return out
}

func modelHeadlines(headlines []trimeline.Headline) []models.TrimelineHeadline {
	out := make([]models.TrimelineHeadline, 0, len(headlines))
	for _, h := range headlines {
		out = append(out, models.TrimelineHeadline{Slug: h.Slug, Title: h.Title, PubDate: h.PubDate.Format(time.RFC3339)})
	}
	return out
}

func trimelineActor(r *http.Request) string {
	if user, ok := middleware.UserFromContext(r.Context()); ok && user != nil {
		return user.Name
	}
	return ""
}
