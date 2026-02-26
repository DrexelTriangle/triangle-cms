package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type ResponsePayload struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func Users(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	payload := ResponsePayload{
		Status:  "OK",
		Message: "Users endpoint hit",
		Code:    http.StatusOK,
	}

	err := json.NewEncoder(w).Encode(payload)
	if err != nil {
		slog.Error("error encoding json", "error", err)
		http.Error(w, "500 - Internal Server Error", http.StatusInternalServerError)
	}
}
