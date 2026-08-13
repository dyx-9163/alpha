package httpapi

import (
	"context"
	"net/http"

	"aifar-deployment/backend/internal/auditkit"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/resource"
	"aifar-deployment/backend/internal/worker"
)

func (a *API) listResources(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListResources()
	respond(w, out, err)
}

func (a *API) rescanResources(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	actor := currentUser(r).Username
	task, err := a.tasks.StartWithLanguage("resources.scan", a.cfg.ResourceDir, actor, lang, func(ctx context.Context, log worker.Logger) error {
		const target = "resources"
		log.PlanTarget(target)
		log.PlanStep(target, "scan-resources", i18n.Text(lang, "api.resourceScanResourcesStep"), 1)
		log.StartTarget(target)
		log.StartStep(target, "scan-resources", i18n.Text(lang, "api.resourceScanResourcesStep"), 1)
		if err := resource.ScanAndSave(a.store, a.cfg.ResourceDir); err != nil {
			log.FinishStep(target, "scan-resources", "failed", err.Error())
			log.FinishTarget(target, "failed", err.Error())
			return err
		}
		log.FinishStep(target, "scan-resources", "success", "")
		log.FinishTarget(target, "success", "")
		_ = auditkit.Record(a.store, auditkit.Event{
			Actor:   actor,
			Action:  "resources.scan",
			Target:  a.cfg.ResourceDir,
			Status:  "success",
			Message: i18n.Text(lang, "api.resourceScanCompleted"),
		})
		return nil
	})
	respondTask(w, task, err)
}
