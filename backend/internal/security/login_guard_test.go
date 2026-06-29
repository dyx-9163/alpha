package security

import (
	"testing"
	"time"
)

func TestLoginGuardLocksAfterMaxFailures(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	guard := NewLoginGuard(2, time.Minute)
	guard.now = func() time.Time { return now }

	if _, locked := guard.RecordFailure("Admin|127.0.0.1"); locked {
		t.Fatalf("first failure should not lock")
	}
	until, locked := guard.RecordFailure("admin|127.0.0.1")
	if !locked {
		t.Fatalf("second failure should lock")
	}
	if !until.Equal(now.Add(time.Minute)) {
		t.Fatalf("unexpected lockout time: %s", until)
	}
	if got, locked := guard.LockedUntil("ADMIN|127.0.0.1"); !locked || !got.Equal(until) {
		t.Fatalf("expected locked key, got locked=%v until=%s", locked, got)
	}
}

func TestLoginGuardSuccessClearsFailures(t *testing.T) {
	guard := NewLoginGuard(2, time.Minute)

	guard.RecordFailure("admin")
	guard.RecordSuccess("admin")
	if _, locked := guard.RecordFailure("admin"); locked {
		t.Fatalf("failure after success should restart counter")
	}
}

func TestLoginGuardUnlocksAfterWindow(t *testing.T) {
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	guard := NewLoginGuard(1, time.Minute)
	guard.now = func() time.Time { return now }

	guard.RecordFailure("admin")
	now = now.Add(time.Minute + time.Second)
	if _, locked := guard.LockedUntil("admin"); locked {
		t.Fatalf("expected lockout to expire")
	}
}
