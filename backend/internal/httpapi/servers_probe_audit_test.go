package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	serverdomain "aifar-deployment/backend/internal/servers"
	"aifar-deployment/backend/internal/store"
)

func TestServerProbeDoesNotWriteAuditLog(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.servers = serverdomain.NewService(db, successfulProbe{}, "/aifar/apps")
	token := issueTestToken(t, db, secret, "owner", "owner")
	server, err := db.SaveServer(store.Server{
		Name:     "node-1",
		Host:     "127.0.0.1",
		Port:     22,
		Username: "root",
		AuthType: "password",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v2/servers/"+server.ID+"/probe", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("expected probe accepted, got %d body=%s", rec.Code, rec.Body.String())
	}
	items, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Action == "servers.probe" {
			t.Fatalf("server probe should not be written to audit logs, got %+v", item)
		}
	}
}

type successfulProbe struct{}

func (successfulProbe) Probe(context.Context, store.Server) error {
	return nil
}
