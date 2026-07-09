package appmeta

import (
	"errors"
	"testing"
	"time"
)

func TestMetadataParseAccessorsAndMarshal(t *testing.T) {
	metadata, err := ParseStrict(`{"endpoint":"10.0.0.1:3306","port":"3306","enabled":"true","services":["api",2],"clusterId":"c1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := String(metadata, "endpoint", ""); got != "10.0.0.1:3306" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := Int(metadata, "port", 0); got != 3306 {
		t.Fatalf("port=%d", got)
	}
	if got := Bool(metadata, "enabled", false); !got {
		t.Fatalf("enabled=%v", got)
	}
	services := StringSlice(metadata, "services")
	if len(services) != 2 || services[0] != "api" || services[1] != "2" {
		t.Fatalf("services=%+v", services)
	}
	if ClusterID(metadata) != "c1" {
		t.Fatalf("cluster id not parsed: %+v", metadata)
	}
	if Marshal(metadata) == "{}" {
		t.Fatalf("expected non-empty marshal")
	}
}

func TestMetadataLastCheckAndInstallFailed(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	metadata := WithLastCheck(nil, LastCheck{Status: "healthy", Message: "ok", CheckedAt: now, Details: map[string]any{"role": "primary"}})
	check, ok := LastCheckFrom(metadata)
	if !ok || check.Status != "healthy" || check.Message != "ok" || check.CheckedAt.IsZero() || check.Details["role"] != "primary" {
		t.Fatalf("unexpected last check: %+v ok=%v", check, ok)
	}
	failed := MarkInstallFailed(metadata, "tsk-1", errors.New("boom"))
	if !Bool(failed, "installFailed", false) || String(failed, "taskId", "") != "tsk-1" || String(failed, "installError", "") != "boom" {
		t.Fatalf("unexpected failed metadata: %+v", failed)
	}
	if _, ok := metadata["installFailed"]; ok {
		t.Fatalf("expected MarkInstallFailed to clone metadata")
	}
}
