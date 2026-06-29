package auditkit

import (
	"aifar-deployment/backend/internal/logmask"
	"aifar-deployment/backend/internal/store"
)

type Store interface {
	AddAudit(actor, action, target, status, message string) error
}

type Event struct {
	Actor   string
	Action  string
	Target  string
	Status  string
	Message string
}

func Record(s Store, event Event) error {
	return s.AddAudit(
		event.Actor,
		event.Action,
		logmask.Mask(event.Target),
		event.Status,
		logmask.Mask(event.Message),
	)
}

func FromStoreAudit(a store.Audit) Event {
	return Event{
		Actor:   a.Actor,
		Action:  a.Action,
		Target:  a.Target,
		Status:  a.Status,
		Message: a.Message,
	}
}
