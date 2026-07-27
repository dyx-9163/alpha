package mysql

import (
	"encoding/json"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	strictSchemaName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	secretShape      = regexp.MustCompile(`(?i)(password|passwd|secret|token|private[ _-]?key|credential)\s*[:=]`)
)

var fixedSystemSchemas = []string{
	"information_schema",
	"mysql",
	"mysql_innodb_cluster_metadata",
	"performance_schema",
	"sys",
}

// NormalizeBackupManifest validates a portable backup manifest and returns a
// deterministic copy. Callers must persist only the returned value.
func NormalizeBackupManifest(manifest BackupManifest) (BackupManifest, error) {
	if strings.TrimSpace(manifest.App) != "mysql" {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	manifest.Topology = strings.ToLower(strings.TrimSpace(manifest.Topology))
	if manifest.Topology != "standalone" && manifest.Topology != "innodb-cluster" {
		return BackupManifest{}, mysqlOperationError(MySQLBackupUnsupportedTopology)
	}
	if !manifest.Consistent || manifest.CreatedAt.IsZero() ||
		strings.TrimSpace(manifest.BackupID) == "" ||
		strings.TrimSpace(manifest.InstanceID) == "" ||
		strings.TrimSpace(manifest.SourceServerID) == "" ||
		strings.TrimSpace(manifest.SourceServerUUID) == "" ||
		strings.TrimSpace(manifest.MySQLVersion) == "" ||
		strings.TrimSpace(manifest.MySQLShellVersion) == "" ||
		strings.TrimSpace(manifest.TaskID) == "" ||
		!validEndpoint(manifest.SourceEndpoint) {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	if containsSecretShape(manifest) {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}

	schemas, err := normalizeBusinessSchemas(manifest.Schemas)
	if err != nil {
		return BackupManifest{}, err
	}
	if !hasFixedSystemSchemaExclusions(manifest.ExcludedSchemas) {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	manifest.Schemas = schemas
	manifest.ExcludedSchemas = append([]string(nil), fixedSystemSchemas...)

	switch manifest.Topology {
	case "standalone":
		if strings.TrimSpace(manifest.ClusterID) != "" || len(manifest.Members) != 0 {
			return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
	case "innodb-cluster":
		if strings.TrimSpace(manifest.ClusterID) == "" {
			return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		members, err := normalizeClusterMembers(manifest.Members, manifest.SourceServerID, manifest.SourceEndpoint)
		if err != nil {
			return BackupManifest{}, err
		}
		manifest.Members = members
	}
	routers, err := normalizeRouters(manifest.Routers)
	if err != nil {
		return BackupManifest{}, err
	}
	manifest.Routers = routers
	return manifest, nil
}

// CanonicalBackupManifestJSON validates then serializes a deterministic,
// non-secret manifest for the backup repository.
func CanonicalBackupManifestJSON(manifest BackupManifest) ([]byte, error) {
	normalized, err := NormalizeBackupManifest(manifest)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// ValidateRestoreCompatibility enforces v1's exact topology and full MySQL
// version matching rules before any target schema work can begin.
func ValidateRestoreCompatibility(manifest BackupManifest, backupType, targetTopology, targetMySQLVersion string) error {
	normalized, err := NormalizeBackupManifest(manifest)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(backupType) {
	case "logical-full", "pre-restore":
	default:
		return mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	if strings.ToLower(strings.TrimSpace(targetTopology)) != normalized.Topology {
		return mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	if strings.TrimSpace(targetMySQLVersion) != normalized.MySQLVersion {
		return mysqlOperationError(MySQLRestoreVersionIncompatible)
	}
	return nil
}

func normalizeBusinessSchemas(schemas []string) ([]string, error) {
	seen := make(map[string]struct{}, len(schemas))
	result := make([]string, 0, len(schemas))
	for _, raw := range schemas {
		schema := strings.TrimSpace(raw)
		if !strictSchemaName.MatchString(schema) || isSystemSchema(schema) {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		if _, ok := seen[schema]; ok {
			continue
		}
		seen[schema] = struct{}{}
		result = append(result, schema)
	}
	if len(result) == 0 {
		return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	sort.Strings(result)
	return result, nil
}

func isSystemSchema(schema string) bool {
	for _, systemSchema := range fixedSystemSchemas {
		if schema == systemSchema {
			return true
		}
	}
	return false
}

func hasFixedSystemSchemaExclusions(schemas []string) bool {
	if len(schemas) != len(fixedSystemSchemas) {
		return false
	}
	seen := make(map[string]struct{}, len(schemas))
	for _, schema := range schemas {
		if _, ok := seen[schema]; ok || !isSystemSchema(schema) {
			return false
		}
		seen[schema] = struct{}{}
	}
	return len(seen) == len(fixedSystemSchemas)
}

func normalizeClusterMembers(members []ClusterMemberRef, sourceServerID, sourceEndpoint string) ([]ClusterMemberRef, error) {
	if len(members) < 3 {
		return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	instances := make(map[string]struct{}, len(members))
	servers := make(map[string]struct{}, len(members))
	endpoints := make(map[string]struct{}, len(members))
	primaryCount := 0
	var primary ClusterMemberRef
	result := append([]ClusterMemberRef(nil), members...)
	for index := range result {
		member := &result[index]
		member.Role = strings.ToUpper(strings.TrimSpace(member.Role))
		member.Status = strings.ToUpper(strings.TrimSpace(member.Status))
		if strings.TrimSpace(member.InstanceID) == "" || strings.TrimSpace(member.ServerID) == "" || !validEndpoint(member.Endpoint) {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		if _, ok := instances[member.InstanceID]; ok {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		if _, ok := servers[member.ServerID]; ok {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		if _, ok := endpoints[member.Endpoint]; ok {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		instances[member.InstanceID] = struct{}{}
		servers[member.ServerID] = struct{}{}
		endpoints[member.Endpoint] = struct{}{}
		if member.Status != "ONLINE" {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		if member.Role == "PRIMARY" {
			primaryCount++
			primary = *member
		}
	}
	if primaryCount != 1 {
		return nil, mysqlOperationError(MySQLBackupPrimaryNotFound)
	}
	if primary.ServerID != sourceServerID || primary.Endpoint != sourceEndpoint {
		return nil, mysqlOperationError(MySQLBackupPrimaryNotFound)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InstanceID == result[j].InstanceID {
			return result[i].ServerID < result[j].ServerID
		}
		return result[i].InstanceID < result[j].InstanceID
	})
	return result, nil
}

func normalizeRouters(routers []RouterRef) ([]RouterRef, error) {
	result := append([]RouterRef(nil), routers...)
	seen := make(map[string]struct{}, len(result))
	for _, router := range result {
		if strings.TrimSpace(router.InstanceID) == "" || strings.TrimSpace(router.ServerID) == "" || !validEndpoint(router.Endpoint) {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		if _, ok := seen[router.InstanceID]; ok {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		seen[router.InstanceID] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InstanceID == result[j].InstanceID {
			return result[i].ServerID < result[j].ServerID
		}
		return result[i].InstanceID < result[j].InstanceID
	})
	return result, nil
}

func validEndpoint(endpoint string) bool {
	if strings.TrimSpace(endpoint) != endpoint || strings.Contains(endpoint, "@") || secretShape.MatchString(endpoint) {
		return false
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" {
		return false
	}
	parsedPort, err := strconv.Atoi(port)
	return err == nil && parsedPort > 0 && parsedPort <= 65535
}

func containsSecretShape(value any) bool {
	switch typed := value.(type) {
	case string:
		return secretShape.MatchString(typed) || strings.Contains(typed, "://") && strings.Contains(typed, "@")
	case BackupManifest:
		return containsSecretShape(typed.BackupID) || containsSecretShape(typed.App) || containsSecretShape(typed.Topology) ||
			containsSecretShape(typed.InstanceID) || containsSecretShape(typed.ClusterID) || containsSecretShape(typed.SourceServerID) ||
			containsSecretShape(typed.SourceEndpoint) || containsSecretShape(typed.SourceServerUUID) || containsSecretShape(typed.MySQLVersion) ||
			containsSecretShape(typed.MySQLShellVersion) || containsSecretShape(typed.Schemas) || containsSecretShape(typed.ExcludedSchemas) ||
			containsSecretShape(typed.GTIDExecuted) || containsSecretShape(typed.Members) || containsSecretShape(typed.Routers) || containsSecretShape(typed.TaskID)
	case []string:
		for _, item := range typed {
			if containsSecretShape(item) {
				return true
			}
		}
	case []ClusterMemberRef:
		for _, item := range typed {
			if containsSecretShape(item.InstanceID) || containsSecretShape(item.ServerID) || containsSecretShape(item.Endpoint) || containsSecretShape(item.Role) || containsSecretShape(item.Status) {
				return true
			}
		}
	case []RouterRef:
		for _, item := range typed {
			if containsSecretShape(item.InstanceID) || containsSecretShape(item.ServerID) || containsSecretShape(item.Endpoint) || containsSecretShape(item.Status) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if secretShape.MatchString(key) || containsSecretShape(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsSecretShape(item) {
				return true
			}
		}
	}
	return false
}
