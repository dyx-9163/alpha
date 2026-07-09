package taskmeta

import "strings"

type Descriptor struct {
	Category  string
	Trackable bool
}

func Describe(taskType string) Descriptor {
	normalized := strings.ToLower(strings.TrimSpace(taskType))
	if normalized == "" {
		return Descriptor{Category: "other"}
	}
	switch {
	case strings.HasPrefix(normalized, "apps."):
		return Descriptor{Category: "apps", Trackable: isAppLifecycleTask(normalized)}
	case strings.HasPrefix(normalized, "aifar."):
		return Descriptor{Category: "apps", Trackable: true}
	case strings.HasPrefix(normalized, "database."):
		return Descriptor{Category: "database", Trackable: true}
	case strings.HasPrefix(normalized, "storage."):
		return Descriptor{Category: "storage", Trackable: true}
	case strings.HasPrefix(normalized, "containers."):
		return Descriptor{Category: "containers", Trackable: true}
	case strings.HasPrefix(normalized, "servers."):
		return Descriptor{Category: "servers", Trackable: normalized == "servers.probe"}
	case strings.HasPrefix(normalized, "resources."), strings.HasPrefix(normalized, "resource."):
		return Descriptor{Category: "resources", Trackable: true}
	case strings.HasPrefix(normalized, "maintenance."):
		return Descriptor{Category: "maintenance", Trackable: true}
	case strings.HasPrefix(normalized, "terminal."):
		return Descriptor{Category: "terminal"}
	case strings.HasPrefix(normalized, "audit."):
		return Descriptor{Category: "audit"}
	default:
		return Descriptor{Category: categoryFromPrefix(normalized)}
	}
}

func isAppLifecycleTask(taskType string) bool {
	if taskType == "apps.delete.batch" {
		return true
	}
	if strings.Contains(taskType, ".config.") {
		return true
	}
	suffixes := []string{
		".install",
		".delete",
		".uninstall",
		".check",
		".update",
		".update-artifact",
		".update-artifact-bundle",
		".rollback",
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(taskType, suffix) {
			return true
		}
	}
	return false
}

func categoryFromPrefix(taskType string) string {
	prefix := strings.Split(taskType, ".")[0]
	switch prefix {
	case "", "mysql", "redis":
		return "database"
	case "minio":
		return "storage"
	case "toolbox":
		return "resources"
	case "apps", "servers", "containers", "database", "storage", "resources", "terminal", "audit", "maintenance":
		return prefix
	default:
		return "other"
	}
}
