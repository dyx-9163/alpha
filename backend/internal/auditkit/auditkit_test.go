package auditkit

import "testing"

type fakeAuditStore struct {
	actor   string
	action  string
	target  string
	status  string
	message string
}

func (s *fakeAuditStore) AddAudit(actor, action, target, status, message string) error {
	s.actor = actor
	s.action = action
	s.target = target
	s.status = status
	s.message = message
	return nil
}

func TestRecordMasksSensitiveTargetAndMessage(t *testing.T) {
	fake := &fakeAuditStore{}

	if err := Record(fake, Event{
		Actor:   "admin",
		Action:  "servers.save",
		Target:  "ssh://root:password=secret@192.168.1.10",
		Status:  "success",
		Message: `{"token":"abc","message":"saved"}`,
	}); err != nil {
		t.Fatalf("record audit: %v", err)
	}

	if fake.target == "" || fake.message == "" {
		t.Fatalf("expected audit fields to be recorded")
	}
	if fake.target == "ssh://root:password=secret@192.168.1.10" || fake.message == `{"token":"abc","message":"saved"}` {
		t.Fatalf("expected sensitive audit fields to be masked: %#v", fake)
	}
}
