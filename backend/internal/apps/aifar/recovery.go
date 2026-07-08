package aifar

import (
	"time"
)

func RecoverInterruptedOrchestrationLocks(s Store) (int, error) {
	instances, err := s.ListAppInstances()
	if err != nil {
		return 0, err
	}
	recovered := 0
	now := time.Now().UTC()
	lockStore, hasLockStore := s.(aifarOrchestrationLockStore)
	for _, instance := range instances {
		if instance.App != AppName {
			continue
		}
		metadata := metadataFromInstance(instance)
		_, hasGlobal := metadata["orchestrationLock"]
		locks := serviceOrchestrationLocksFromMetadata(metadata)
		structuredRecovered := 0
		if hasLockStore {
			count, err := lockStore.RecoverAIFAROrchestrationLocks(instance.ID, "aifar-server startup recovered interrupted orchestration")
			if err != nil {
				return recovered, err
			}
			structuredRecovered = count
		}
		if !hasGlobal && len(locks) == 0 && structuredRecovered == 0 {
			continue
		}
		delete(metadata, "orchestrationLock")
		delete(metadata, "orchestrationLocks")
		metadata["lastOrchestrationRecovery"] = map[string]any{
			"recoveredAt": now.Format(time.RFC3339),
			"reason":      "aifar-server startup recovered interrupted orchestration",
			"locks":       structuredRecovered,
		}
		if err := saveMetadata(s, instance, metadata); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}
