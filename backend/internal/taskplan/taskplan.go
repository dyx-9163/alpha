package taskplan

import "strings"

type Store interface {
	UpsertTaskTarget(taskID, target, status, errText string) error
	UpsertTaskStep(taskID, target, name, title string, order int, status, errText string) error
}

type Step struct {
	Target string
	Name   string
	Title  string
	Order  int
}

func StorePlan(store Store, taskID string, plan []Step) error {
	plannedTargets := map[string]bool{}
	for _, step := range plan {
		target := strings.TrimSpace(step.Target)
		if target != "" && !plannedTargets[target] {
			if err := store.UpsertTaskTarget(taskID, target, "pending", ""); err != nil {
				return err
			}
			plannedTargets[target] = true
		}
		name := strings.TrimSpace(step.Name)
		if name == "" {
			continue
		}
		if err := store.UpsertTaskStep(taskID, target, name, step.Title, step.Order, "pending", ""); err != nil {
			return err
		}
	}
	return nil
}
