package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
)

const (
	operationLockMutation = "mutate"
	operationLockTTL      = time.Hour
)

type operationLockSpec struct {
	Scope      string
	ResourceID string
	Operation  string
	Metadata   string
}

func (a *API) acquireTaskOperationLocks(w http.ResponseWriter, lang string, task store.Task, specs []operationLockSpec) ([]store.OperationLock, bool) {
	locks := make([]store.OperationLock, 0, len(specs))
	seen := map[string]bool{}
	for _, spec := range specs {
		spec.Scope = strings.TrimSpace(spec.Scope)
		spec.ResourceID = strings.TrimSpace(spec.ResourceID)
		spec.Operation = strings.TrimSpace(spec.Operation)
		if spec.Operation == "" {
			spec.Operation = operationLockMutation
		}
		if spec.Scope == "" || spec.ResourceID == "" {
			continue
		}
		key := spec.Scope + "\x00" + spec.ResourceID + "\x00" + spec.Operation
		if seen[key] {
			continue
		}
		seen[key] = true
		lock, err := a.store.AcquireOperationLock(store.OperationLock{
			Scope:       spec.Scope,
			ResourceID:  spec.ResourceID,
			Operation:   spec.Operation,
			OwnerTaskID: task.ID,
			Owner:       task.CreatedBy,
			ExpiresAt:   time.Now().UTC().Add(operationLockTTL),
			Metadata:    spec.Metadata,
		})
		if err != nil {
			a.releaseOperationLocks(locks)
			_ = a.store.DeleteTask(task.ID)
			var conflict store.OperationLockConflict
			if errors.As(err, &conflict) {
				writeError(w, http.StatusConflict, "OPERATION_LOCKED", i18n.Text(lang, "api.operationLocked", conflict.Lock.ResourceID), map[string]any{
					"scope":       conflict.Lock.Scope,
					"resourceId":  conflict.Lock.ResourceID,
					"operation":   conflict.Lock.Operation,
					"ownerTaskId": conflict.Lock.OwnerTaskID,
					"expiresAt":   conflict.Lock.ExpiresAt,
				})
				return nil, false
			}
			writeError(w, http.StatusInternalServerError, "OPERATION_LOCK_FAILED", err.Error(), nil)
			return nil, false
		}
		locks = append(locks, lock)
	}
	return locks, true
}

func (a *API) releaseOperationLocks(locks []store.OperationLock) {
	for _, lock := range locks {
		_, _ = a.store.ReleaseOperationLock(lock.ID)
	}
}

func appInstallOperationLockSpecs(app string, serverIDs []string) []operationLockSpec {
	family := lifecycleAppFamily(app)
	specs := make([]operationLockSpec, 0, len(serverIDs))
	for _, serverID := range serverIDs {
		serverID = strings.TrimSpace(serverID)
		if serverID == "" {
			continue
		}
		specs = append(specs, operationLockSpec{
			Scope:      "app-target",
			ResourceID: family + ":" + serverID,
			Operation:  operationLockMutation,
			Metadata: operationLockMetadata(map[string]any{
				"action":   "install",
				"app":      app,
				"serverId": serverID,
			}),
		})
	}
	return specs
}

func appInstanceOperationLockSpecs(action string, instances []store.AppInstance) []operationLockSpec {
	specs := make([]operationLockSpec, 0, len(instances))
	for _, instance := range instances {
		if strings.TrimSpace(instance.ID) == "" {
			continue
		}
		specs = append(specs, operationLockSpec{
			Scope:      "app-instance",
			ResourceID: instance.ID,
			Operation:  operationLockMutation,
			Metadata: operationLockMetadata(map[string]any{
				"action":     action,
				"app":        instance.App,
				"instanceId": instance.ID,
				"serverId":   instance.ServerID,
			}),
		})
	}
	return specs
}

func operationLockMetadata(fields map[string]any) string {
	data, err := json.Marshal(fields)
	if err != nil {
		return "{}"
	}
	return string(data)
}
