package aifar

import (
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"context"
	"errors"
	"fmt"
)

// MigrationPhases is shared with task planning; a retry always inspects remote state.
var migrationPhases = []string{"preflight", "bootstrap", "readback", "archive", "commit"}

func (m Module) MigrateRuntimeInstance(ctx context.Context, id, reason string, run registry.RunContext) (err error) {
	instance, err := m.service.store.GetAppInstance(id)
	if err != nil {
		return err
	}
	server, err := m.service.store.GetServer(instance.ServerID, true)
	if err != nil {
		return err
	}
	type stepLogger interface {
		StartStep(string, string, string, int)
		FinishStep(string, string, string, string)
	}
	steps, _ := run.Log.(stepLogger)
	active := ""
	progress := func(name string) {
		if steps != nil {
			if active != "" {
				steps.FinishStep(id, active, "success", "")
			}
			for i, p := range migrationPhases {
				if p == name {
					steps.StartStep(id, name, i18n.Text(run.Language, "aifar.migration."+name), i+1)
				}
			}
		}
		active = name
	}
	defer func() {
		var reason interface{ ReasonCode() string }
		if err != nil && errors.As(err, &reason) {
			err = fmt.Errorf("%w [%s]", err, reason.ReasonCode())
		}
		if steps != nil && active != "" {
			status, detail := "success", ""
			if err != nil {
				status = "failed"
				detail = i18n.Text(run.Language, "aifar.migration.failed")
			}
			steps.FinishStep(id, active, status, detail)
		}
	}()
	var tryCommit func() bool
	if gate, ok := run.Log.(interface{ TryEnterCommit() bool }); ok {
		tryCommit = gate.TryEnterCommit
	}
	return m.service.MigrateRuntimeModel(ctx, RuntimeMigrationRequest{Instance: instance, Server: server, Actor: run.Actor, TaskID: run.TaskID, Reason: reason, Language: run.Language, OnPhase: progress, TryCommit: tryCommit}, run.Log)
}
