package rbac

import "strings"

type Permission string

const (
	SettingsManage   Permission = "settings.manage"
	UsersManage      Permission = "users.manage"
	ResourcesScan    Permission = "resources.scan"
	ServersManage    Permission = "servers.manage"
	TerminalConnect  Permission = "terminal.connect"
	TasksManage      Permission = "tasks.manage"
	AuditManage      Permission = "audit.manage"
	AppsManage       Permission = "apps.manage"
	ContainersManage Permission = "containers.manage"
	DatabaseManage   Permission = "database.manage"
	StorageManage    Permission = "storage.manage"
)

var rolePermissions = map[string]map[Permission]struct{}{
	"owner":    allPermissions(),
	"admin":    allPermissions(),
	"operator": permissionSet(ResourcesScan, ServersManage, TerminalConnect, TasksManage, AppsManage, ContainersManage, DatabaseManage, StorageManage),
	"viewer":   permissionSet(),
	"auditor":  permissionSet(),
}

func Allows(role string, permission Permission) bool {
	if permission == "" {
		return true
	}
	permissions, ok := rolePermissions[normalizeRole(role)]
	if !ok {
		return false
	}
	_, ok = permissions[permission]
	return ok
}

func Permissions(role string) []Permission {
	permissions, ok := rolePermissions[normalizeRole(role)]
	if !ok {
		return nil
	}
	out := make([]Permission, 0, len(permissions))
	for permission := range permissions {
		out = append(out, permission)
	}
	return out
}

func ValidRole(role string) bool {
	_, ok := rolePermissions[normalizeRole(role)]
	return ok
}

func NormalizeRole(role string) string {
	return normalizeRole(role)
}

func normalizeRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

func allPermissions() map[Permission]struct{} {
	return permissionSet(
		SettingsManage,
		UsersManage,
		ResourcesScan,
		ServersManage,
		TerminalConnect,
		TasksManage,
		AuditManage,
		AppsManage,
		ContainersManage,
		DatabaseManage,
		StorageManage,
	)
}

func permissionSet(permissions ...Permission) map[Permission]struct{} {
	out := make(map[Permission]struct{}, len(permissions))
	for _, permission := range permissions {
		out[permission] = struct{}{}
	}
	return out
}
