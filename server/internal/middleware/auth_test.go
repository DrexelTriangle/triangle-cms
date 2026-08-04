package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"server/internal/models"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireEditorAllowsEditorsAndAdmins(t *testing.T) {
	// Sections management sits behind this: editors run the newsroom day to
	// day, so locking section config to admins meant the person who noticed a
	// broken section was not the person who could fix it.
	for name, tc := range map[string]struct {
		user     *models.User
		wantCode int
	}{
		"editor":       {&models.User{Role: models.RoleEditor}, http.StatusOK},
		"admin":        {&models.User{Role: models.RoleAdmin}, http.StatusOK},
		"blank role":   {&models.User{Role: models.Role("")}, http.StatusForbidden},
		"unknown role": {&models.User{Role: models.Role("viewer")}, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/taxonomy", nil)
			req = req.WithContext(ContextWithUser(req.Context(), tc.user))
			rec := httptest.NewRecorder()
			RequireEditor(okHandler()).ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("%s = %d, want %d", name, rec.Code, tc.wantCode)
			}
		})
	}
}

func TestRequireEditorRejectsAnonymous(t *testing.T) {
	rec := httptest.NewRecorder()
	RequireEditor(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/taxonomy", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("anonymous = %d, want 403", rec.Code)
	}
}

func TestRequireAdminStillExcludesEditors(t *testing.T) {
	// Widening sections must not have widened account administration with it.
	req := httptest.NewRequest(http.MethodPatch, "/v1/users/1", nil)
	req = req.WithContext(ContextWithUser(req.Context(), &models.User{Role: models.RoleEditor}))
	rec := httptest.NewRecorder()
	RequireAdmin(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("editor against RequireAdmin = %d, want 403", rec.Code)
	}
}
