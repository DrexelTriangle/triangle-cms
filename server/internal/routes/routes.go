package routes

import (
	"net/http"
	"server/internal/handlers"
)

func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /users", handlers.Users)
	mux.HandleFunc("GET /structs", handlers.SearchStructs)
}
