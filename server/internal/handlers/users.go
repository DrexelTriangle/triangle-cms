package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	db "server/internal/database"
	"server/internal/middleware"
	"server/internal/models"
)

// @Summary Get current user
// @Tags users
// @Produce json
// @Success 200 {object} models.User
// @Failure 401 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/users/me [get]
func GetMe(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.UserFromContext(r.Context())
	if !ok || user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// @Summary List all users
// @Tags users
// @Produce json
// @Success 200 {array} models.User
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/users [get]
func GetUsers(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := db.ListUsers(r.Context(), conn)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, users)
	}
}

// @Summary Update user role
// @Tags users
// @Accept json
// @Param id path int true "User ID"
// @Param body body models.UserRolePatchRequest true "Role update"
// @Success 204
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /v1/users/{id} [patch]
func PatchUser(conn *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user id")
			return
		}

		var body models.UserRolePatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Role != models.RoleEditor && body.Role != models.RoleAdmin {
			writeError(w, http.StatusBadRequest, "role must be editor or admin")
			return
		}

		if _, err := db.GetUserByID(r.Context(), conn, id); err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "user not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if err := db.UpdateUserRole(r.Context(), conn, id, body.Role); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
