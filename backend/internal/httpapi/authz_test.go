package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/auth"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/security"
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

func TestRepeatedFailedLoginIsLockedAndAudited(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	api.auth = security.NewLoginGuard(2, time.Minute)
	if err := db.ResetUserPassword("owner", "correct-password"); err != nil {
		t.Fatal(err)
	}

	first := postLogin(api, "owner", "wrong-password")
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("expected first failure 401, got %d body=%s", first.Code, first.Body.String())
	}
	second := postLogin(api, "owner", "wrong-password")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second failure to lock with 429, got %d body=%s", second.Code, second.Body.String())
	}
	third := postLogin(api, "owner", "correct-password")
	if third.Code != http.StatusTooManyRequests {
		t.Fatalf("expected locked login to remain 429, got %d body=%s", third.Code, third.Body.String())
	}
	assertAuditExists(t, db, "auth.login.locked", "failed", "owner", "owner")
}

func TestSuccessfulLoginClearsFailureCounter(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	api.auth = security.NewLoginGuard(2, time.Minute)
	if err := db.ResetUserPassword("owner", "correct-password"); err != nil {
		t.Fatal(err)
	}

	if rec := postLogin(api, "owner", "wrong-password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected first failure 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := postLogin(api, "owner", "correct-password"); rec.Code != http.StatusOK {
		t.Fatalf("expected success 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := postLogin(api, "owner", "wrong-password"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected counter reset after success, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSecurityHeadersAreApplied(t *testing.T) {
	api, _, _ := newAuthzTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings", nil)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	for key, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(key); got != want {
			t.Fatalf("expected %s=%q, got %q", key, want, got)
		}
	}
	if got := rec.Header().Get("Permissions-Policy"); got == "" {
		t.Fatalf("expected Permissions-Policy header")
	}
}

func TestRequestBodyLimitReturnsPayloadTooLarge(t *testing.T) {
	api, _, _ := newAuthzTestAPI(t)
	api.cfg.MaxRequestBodyBytes = 12

	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(`{"username":"owner","password":"too-large"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSettingsExposeSecurityLimits(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.cfg.MaxRequestBodyBytes = 2048
	api.cfg.AuthMaxFailures = 7
	api.cfg.AuthLockoutSeconds = 60
	api.cfg.AuditRetentionDays = 120
	api.cfg.TaskRetentionDays = 45
	api.cfg.DatabaseBackupDir = filepath.Join(t.TempDir(), "control-plane-backups")
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["maxRequestBodyBytes"] != float64(2048) || body["authMaxFailures"] != float64(7) || body["authLockoutSeconds"] != float64(60) ||
		body["auditRetentionDays"] != float64(120) || body["taskRetentionDays"] != float64(45) || body["databaseBackupDir"] != api.cfg.DatabaseBackupDir {
		t.Fatalf("security limits missing from settings response: %+v", body)
	}
}

func TestRetentionCleanupStartsTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.cfg.AuditRetentionDays = 1
	api.cfg.TaskRetentionDays = 1
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/maintenance/retention/run", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	taskID, _ := body["taskId"].(string)
	if taskID == "" {
		t.Fatalf("expected taskId in response: %+v", body)
	}
	waitForTaskStatus(t, db, taskID, "success")
	assertAuditExists(t, db, "maintenance.retention.run", "running", "owner", "control-plane")
}

func TestDatabaseBackupStartsTaskAndCreatesFile(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/maintenance/database-backup/run", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	taskID, _ := body["taskId"].(string)
	if taskID == "" {
		t.Fatalf("expected taskId in response: %+v", body)
	}
	waitForTaskStatus(t, db, taskID, "success")
	files, err := filepath.Glob(filepath.Join(api.cfg.DatabaseBackupDir, "aifar-control-plane-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one backup file, got %+v", files)
	}
	backup, err := store.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backup.UserByUsername("owner"); err != nil {
		_ = backup.Close()
		t.Fatalf("expected backed up owner to be readable: %v", err)
	}
	if err := backup.Close(); err != nil {
		t.Fatal(err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v2/maintenance/database-backups", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	api.Router().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected backup list 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listBody struct {
		Items []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Items) != 1 || listBody.Items[0].Name == "" || listBody.Items[0].SHA256 == "" {
		t.Fatalf("expected one listed backup with checksum, got %+v", listBody.Items)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v2/maintenance/database-backups", strings.NewReader(`{"names":["`+listBody.Items[0].Name+`"]}`))
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	deleteReq.Header.Set("Content-Type", "application/json")
	deleteRec := httptest.NewRecorder()
	api.Router().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected backup delete 200, got %d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatalf("expected backup file to be deleted, got %v", err)
	}
	badDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/v2/maintenance/database-backups", strings.NewReader(`{"names":["../aifar-control-plane-bad.db"]}`))
	badDeleteReq.Header.Set("Authorization", "Bearer "+token)
	badDeleteReq.Header.Set("Content-Type", "application/json")
	badDeleteRec := httptest.NewRecorder()
	api.Router().ServeHTTP(badDeleteRec, badDeleteReq)
	if badDeleteRec.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe backup delete 400, got %d body=%s", badDeleteRec.Code, badDeleteRec.Body.String())
	}
	assertAuditExists(t, db, "maintenance.database.backup", "running", "owner", "control-plane")
	assertAuditExists(t, db, "maintenance.database.backup.delete", "success", "owner", listBody.Items[0].Name)
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
		DatabaseBackupDir:     filepath.Join(root, "backups"),
		DefaultDeployDir:      "/aifar/apps",
		DeploymentConcurrency: 1,
		ProviderMode:          "real",
		MaxRequestBodyBytes:   1 << 20,
		AuthMaxFailures:       5,
		AuthLockoutSeconds:    300,
		AuditRetentionDays:    180,
		TaskRetentionDays:     90,
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

func postLogin(api *API, username, password string) *httptest.ResponseRecorder {
	body := `{"username":"` + username + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	return rec
}

func waitForTaskStatus(t *testing.T, db *store.Store, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, _, err := db.GetTask(taskID)
		if err == nil && task.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _, err := db.GetTask(taskID)
	if err != nil {
		t.Fatalf("task %s not found while waiting for status %s: %v", taskID, want, err)
	}
	t.Fatalf("expected task %s status %s, got %s", taskID, want, task.Status)
}
