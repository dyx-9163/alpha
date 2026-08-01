package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestAIFARReleaseDeleteBlockReason(t *testing.T) {
	tests := []struct {
		name     string
		instance store.AppInstance
		target   store.AppRelease
		releases []store.AppRelease
		wantCode string
	}{
		{
			name:     "current revision",
			instance: store.AppInstance{Metadata: `{"currentRevision":"release-current"}`},
			target:   store.AppRelease{ReleaseID: "release-current", Status: "success"},
			wantCode: "AIFAR_RELEASE_DELETE_CURRENT",
		},
		{
			name:     "pending release",
			instance: store.AppInstance{Metadata: `{}`},
			target:   store.AppRelease{ReleaseID: "release-pending", Status: "pending"},
			wantCode: "AIFAR_RELEASE_DELETE_ACTIVE",
		},
		{
			name:     "running release",
			instance: store.AppInstance{Metadata: `{}`},
			target:   store.AppRelease{ReleaseID: "release-running", Status: "running"},
			wantCode: "AIFAR_RELEASE_DELETE_ACTIVE",
		},
		{
			name:     "base release reference",
			instance: store.AppInstance{Metadata: `{}`},
			target:   store.AppRelease{ReleaseID: "release-base", Status: "success"},
			releases: []store.AppRelease{{ReleaseID: "release-child", ManifestJSON: `{"baseReleaseId":"release-base"}`}},
			wantCode: "AIFAR_RELEASE_DELETE_REFERENCED",
		},
		{
			name:     "rollback reference",
			instance: store.AppInstance{Metadata: `{}`},
			target:   store.AppRelease{ReleaseID: "release-target", Status: "failed"},
			releases: []store.AppRelease{{ReleaseID: "release-rollback", ManifestJSON: `{"rollbackTo":"release-target"}`}},
			wantCode: "AIFAR_RELEASE_DELETE_REFERENCED",
		},
		{
			name:     "unreferenced historical release",
			instance: store.AppInstance{Metadata: `{"currentRevision":"release-current"}`},
			target:   store.AppRelease{ReleaseID: "release-old", Status: "failed"},
			releases: []store.AppRelease{{ReleaseID: "release-current", ManifestJSON: `{}`}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := aifarReleaseDeleteBlockReason(tt.instance, tt.target, tt.releases)
			if tt.wantCode == "" {
				if block != nil {
					t.Fatalf("expected deletion to be allowed, got %+v", block)
				}
				return
			}
			if block == nil || block.Code != tt.wantCode {
				t.Fatalf("expected %s, got %+v", tt.wantCode, block)
			}
		})
	}
}

func TestDeleteAIFARReleaseDeletesRecordAndWritesAudit(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	instance, err := db.SaveAppInstance(store.AppInstance{App: "aifar", Version: "runtime-v2", Status: "installed", Metadata: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppRelease(store.AppRelease{InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: "release-old", Status: "failed"}); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/instances/"+instance.ID+"/aifar/releases/release-old", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	releases, err := db.ListAppReleases(instance.ID)
	if err != nil || len(releases) != 0 {
		t.Fatalf("expected release to be deleted, got %+v err=%v", releases, err)
	}
	assertAuditExists(t, db, "aifar.release.delete", "success", "owner", instance.ID+":release-old")
}

func TestDeleteAIFARReleaseRejectsCurrentRelease(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	instance, err := db.SaveAppInstance(store.AppInstance{App: "aifar", Version: "runtime-v2", Status: "installed", Metadata: `{"currentRevision":"release-current"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppRelease(store.AppRelease{InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: "release-current", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/instances/"+instance.ID+"/aifar/releases/release-current", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	releases, err := db.ListAppReleases(instance.ID)
	if err != nil || len(releases) != 1 {
		t.Fatalf("expected current release to remain, got %+v err=%v", releases, err)
	}
}

// Production break caught: release history used changedServices alone, so the
// active artifact release appeared rollbackable and could trigger a no-op.
func TestListAIFARReleasesReportsRollbackEligibility(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	instance, err := db.SaveAppInstance(store.AppInstance{
		App: "aifar", Version: "runtime-v2", Status: "installed",
		Metadata: `{"serviceRevisions":{"oauth":"release-current"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := func(releaseID string) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"kind":            "rollout",
			"changedServices": []string{"oauth"},
			"artifacts": map[string]any{
				"oauth": map[string]any{
					"file":       "oauth.jar",
					"sha256":     strings.Repeat("a", 64),
					"remotePath": "/aifar/releases/" + releaseID + "/oauth.jar",
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	for _, release := range []store.AppRelease{
		{InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: "release-current", Status: "success", ManifestJSON: manifest("release-current"), CreatedAt: time.Now().UTC()},
		{InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: "release-old", Status: "success", ManifestJSON: manifest("release-old"), CreatedAt: time.Now().Add(-time.Minute).UTC()},
	} {
		if _, err := db.SaveAppRelease(release); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/apps/instances/"+instance.ID+"/aifar/releases", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []struct {
			ReleaseID                 string   `json:"releaseId"`
			CurrentServices           []string `json:"currentServices"`
			RollbackServices          []string `json:"rollbackServices"`
			RollbackUnavailableReason string   `json:"rollbackUnavailableReason"`
			RollbackAvailable         bool     `json:"rollbackAvailable"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	rows := map[string]struct {
		CurrentServices           []string
		RollbackServices          []string
		RollbackUnavailableReason string
		RollbackAvailable         bool
	}{}
	for _, row := range response.Items {
		rows[row.ReleaseID] = struct {
			CurrentServices           []string
			RollbackServices          []string
			RollbackUnavailableReason string
			RollbackAvailable         bool
		}{row.CurrentServices, row.RollbackServices, row.RollbackUnavailableReason, row.RollbackAvailable}
	}
	current := rows["release-current"]
	if len(current.CurrentServices) != 1 || current.CurrentServices[0] != "oauth" || current.RollbackUnavailableReason != "ALREADY_ACTIVE" || current.RollbackAvailable || len(current.RollbackServices) != 0 {
		t.Fatalf("current release eligibility = %+v", current)
	}
	old := rows["release-old"]
	if len(old.CurrentServices) != 0 || len(old.RollbackServices) != 1 || old.RollbackServices[0] != "oauth" || old.RollbackUnavailableReason != "" || !old.RollbackAvailable {
		t.Fatalf("old release eligibility = %+v", old)
	}
}

// Production break caught: a module that cannot inspect rollback eligibility
// must not retain the former changed-services fallback.
func TestListAIFARReleasesFailsClosedWithoutInspector(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(&fakePlannedLifecycleModule{name: "aifar"})
	instance, err := db.SaveAppInstance(store.AppInstance{App: "aifar", Version: "runtime-v2", Status: "installed", Metadata: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppRelease(store.AppRelease{
		InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: "release-old", Status: "success",
		ManifestJSON: `{"kind":"rollout","changedServices":["oauth"],"artifacts":{"oauth":{"file":"oauth.jar"}}}`,
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/apps/instances/"+instance.ID+"/aifar/releases", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []struct {
			CurrentServices           []string `json:"currentServices"`
			RollbackServices          []string `json:"rollbackServices"`
			RollbackUnavailableReason string   `json:"rollbackUnavailableReason"`
			RollbackAvailable         bool     `json:"rollbackAvailable"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("items = %+v", response.Items)
	}
	row := response.Items[0]
	if row.CurrentServices == nil || row.RollbackServices == nil || len(row.CurrentServices) != 0 || len(row.RollbackServices) != 0 || row.RollbackUnavailableReason != "ARTIFACT_UNAVAILABLE" || row.RollbackAvailable {
		t.Fatalf("unsafe fallback response = %+v", row)
	}
}
