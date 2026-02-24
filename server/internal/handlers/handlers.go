package handlers

import "net/http"

func Users(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("/users response"))
}
