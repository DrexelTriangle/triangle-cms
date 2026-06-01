package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	db "server/internal/database"
	"server/internal/models"
)

// @Summary Get poll counts
// @Tags poll
// @Produce json
// @Success 200 {object} models.PollCountsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll [get]
func GetPoll(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts, err := pollCounts(r, conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll counts")
			return
		}
		writeJSON(w, http.StatusOK, models.PollCountsResponse{Counts: counts})
	})
}

// @Summary Submit poll vote
// @Tags poll
// @Accept json
// @Accept application/x-www-form-urlencoded
// @Produce json
// @Param body body models.PollOptionRequest false "Poll vote payload"
// @Param poll formData string false "Poll vote option (form payload)"
// @Success 200 {object} models.PollCountsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll [post]
func PostPoll(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		option, err := parseSubmittedPollOption(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid poll option")
			return
		}
		if err := incrementPollCount(r, conn, option); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusBadRequest, "Invalid poll option")
				return
			}
			writeError(w, http.StatusInternalServerError, "Failed to update poll")
			return
		}

		counts, err := pollCounts(r, conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll counts")
			return
		}
		writeJSON(w, http.StatusOK, models.PollCountsResponse{Counts: counts})
	})
}

// @Summary Get poll options
// @Tags poll
// @Produce json
// @Success 200 {object} models.PollOptionsResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll/options [get]
func GetPollOptions(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		options, err := pollOptions(r, conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll options")
			return
		}
		writeJSON(w, http.StatusOK, models.PollOptionsResponse{Options: options})
	})
}

// @Summary Get poll title
// @Tags poll
// @Produce json
// @Success 200 {object} models.PollTitleResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /v1/poll/title [get]
func GetPollTitle(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title, err := db.GetPollTitle(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to fetch poll title")
			return
		}
		writeJSON(w, http.StatusOK, models.PollTitleResponse{Title: title})
	})
}

// @Summary Update poll title
// @Tags poll
// @Accept json
// @Produce json
// @Param body body models.PollTitleRequest true "Poll title update"
// @Success 200 {object} models.PollTitleResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/title [patch]
func PatchPollTitle(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollTitleRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		title := strings.TrimSpace(body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "poll title is required")
			return
		}
		if len(title) > 200 {
			writeError(w, http.StatusBadRequest, "poll title too long")
			return
		}
		if err := db.SetPollTitle(r.Context(), conn, title); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update poll title")
			return
		}
		writeJSON(w, http.StatusOK, models.PollTitleResponse{Title: title})
	})
}

// @Summary Add poll option
// @Tags poll
// @Accept json
// @Param body body models.PollOptionRequest true "Poll option"
// @Success 201
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/options [post]
func PostPollOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollOptionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		option := strings.TrimSpace(body.Option)
		if !validPollOption(option) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}

		res, err := conn.ExecContext(r.Context(),
			"INSERT INTO cms_poll_counts (option_name, vote_count) VALUES (?, 0)",
			option,
		)
		if err != nil {
			writeError(w, http.StatusConflict, "poll option already exists")
			return
		}
		id, _ := res.RowsAffected()
		if id == 0 {
			writeError(w, http.StatusConflict, "poll option already exists")
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
}

// @Summary Rename poll option
// @Tags poll
// @Accept json
// @Param body body models.PollOptionRenameRequest true "Poll option rename"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/options [patch]
func PatchPollOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollOptionRenameRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		oldOption := strings.TrimSpace(body.OldOption)
		newOption := strings.TrimSpace(body.NewOption)
		if !validPollOption(oldOption) || !validPollOption(newOption) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}

		res, err := conn.ExecContext(r.Context(),
			"UPDATE cms_poll_counts SET option_name = ? WHERE option_name = ?",
			newOption, oldOption,
		)
		if err != nil {
			writeError(w, http.StatusConflict, "unable to rename poll option")
			return
		}
		rows, err := res.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "unable to rename poll option")
			return
		}
		if rows == 0 {
			writeError(w, http.StatusNotFound, "poll option not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// @Summary Delete poll option
// @Tags poll
// @Accept json
// @Param body body models.PollOptionRequest true "Poll option"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/poll/options [delete]
func DeletePollOption(conn *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body models.PollOptionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		option := strings.TrimSpace(body.Option)
		if !validPollOption(option) {
			writeError(w, http.StatusBadRequest, "invalid poll option")
			return
		}

		res, err := conn.ExecContext(r.Context(), "DELETE FROM cms_poll_counts WHERE option_name = ?", option)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "unable to delete poll option")
			return
		}
		rows, err := res.RowsAffected()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "unable to delete poll option")
			return
		}
		if rows == 0 {
			writeError(w, http.StatusNotFound, "poll option not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func parseSubmittedPollOption(r *http.Request) (string, error) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	switch {
	case strings.Contains(contentType, "application/json"):
		var body models.PollOptionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", err
		}
		return strings.TrimSpace(body.Option), nil
	default:
		if err := r.ParseForm(); err != nil {
			return "", err
		}
		return strings.TrimSpace(r.FormValue("poll")), nil
	}
}

func pollCounts(r *http.Request, conn *sql.DB) (map[string]int64, error) {
	rows, err := conn.QueryContext(r.Context(), "SELECT option_name, vote_count FROM cms_poll_counts ORDER BY option_name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int64{}
	for rows.Next() {
		var option string
		var count int64
		if err := rows.Scan(&option, &count); err != nil {
			return nil, err
		}
		counts[option] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

func pollOptions(r *http.Request, conn *sql.DB) ([]string, error) {
	rows, err := conn.QueryContext(r.Context(), "SELECT option_name FROM cms_poll_counts ORDER BY option_name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	options := make([]string, 0)
	for rows.Next() {
		var option string
		if err := rows.Scan(&option); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return options, nil
}

func incrementPollCount(r *http.Request, conn *sql.DB, option string) error {
	if !validPollOption(option) {
		return sql.ErrNoRows
	}

	res, err := conn.ExecContext(r.Context(),
		"UPDATE cms_poll_counts SET vote_count = vote_count + 1 WHERE option_name = ?",
		option,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func validPollOption(option string) bool {
	trimmed := strings.TrimSpace(option)
	return trimmed != "" && len(trimmed) <= 128
}
