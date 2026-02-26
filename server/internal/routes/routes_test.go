package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegister_UsersRoute(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{
			name:       "get users route",
			method:     http.MethodGet,
			path:       "/users",
			wantStatus: http.StatusOK,
		},
		{
			name:       "post users not allowed",
			method:     http.MethodPost,
			path:       "/users",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "unknown route not found",
			method:     http.MethodGet,
			path:       "/unknown",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d", tt.wantStatus, rec.Code)
			}
		})
	}
}
