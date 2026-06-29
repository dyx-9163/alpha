package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/auth"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

func TestViewerCannotMutateSettings(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "viewer", "viewer")

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", strings.NewReader(`{"language":"en"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	assertAuditExists(t, db, "auth.permission.denied", "failed", "viewer", "settings.manage")
}

func TestOwnerCanMutateSettings(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", strings.NewReader(`{"language":"en"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTokenIsRejectedAfterPasswordReset(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "owner", "owner")
	if err := db.ResetUserPassword("owner", "new-password"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFailedLoginIsAudited(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	if err := db.ResetUserPassword("owner", "correct-password"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(`{"username":"owner","password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	assertAuditExists(t, db, "auth.login", "failed", "owner", "owner")
}

func newAuthzTestAPI(t *testing.T) (*API, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	secret := "test-secret"
	cfg := config.Config{
		JWTSecret:             secret,
		ResourceDir:           filepath.Join(root, "resources"),
		StaticDir:             root,
		DatabasePath:          filepath.Join(root, "aifar.db"),
		DefaultDeployDir:      "/aifar/apps",
		DeploymentConcurrency: 1,
		ProviderMode:          "real",
	}
	return New(cfg, db, worker.NewManager(db)), db, secret
}

func issueTestToken(t *testing.T, db *store.Store, secret, username, role string) string {
	t.Helper()
	if err := db.ResetUserPassword(username, "password"); err != nil {
		t.Fatal(err)
	}
	if role != "owner" {
		if err := db.SetUserRole(username, role); err != nil {
			t.Fatal(err)
		}
	}
	user, err := db.UserByUsername(username)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.IssueToken(secret, user)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func assertAuditExists(t *testing.T, db *store.Store, action, status, actor, target string) {
	t.Helper()
	items, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Action == action && item.Status == status && item.Actor == actor && item.Target == target {
			return
		}
	}
	t.Fatalf("expected audit action=%s status=%s actor=%s target=%s in %+v", action, status, actor, target, items)
}
