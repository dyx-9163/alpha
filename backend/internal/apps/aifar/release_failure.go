package aifar

import (
	"encoding/json"
	"time"

	"aifar-deployment/backend/internal/store"
)

func (s Service) markRecordedReleaseFailed(release *store.AppRelease, cause error) error {
	if release == nil || cause == nil {
		return nil
	}
	releases, ok := s.store.(releaseStore)
	if !ok {
		return nil
	}
	manifest := map[string]any{}
	if release.ManifestJSON != "" {
		if err := json.Unmarshal([]byte(release.ManifestJSON), &manifest); err != nil {
			return err
		}
	}
	failedAt := time.Now().UTC()
	manifest["status"] = "failed"
	manifest["phase"] = "failed"
	manifest["error"] = cause.Error()
	manifest["failedAt"] = failedAt.Format(time.RFC3339Nano)
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	next := *release
	next.Status = "failed"
	next.ManifestJSON = string(raw)
	next.ActivatedAt = time.Time{}
	_, err = releases.SaveAppRelease(next)
	return err
}
