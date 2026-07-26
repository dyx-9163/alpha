package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
