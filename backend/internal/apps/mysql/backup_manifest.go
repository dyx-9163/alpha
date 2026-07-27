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
	strictSchemaName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	secretAssignment  = regexp.MustCompile(`(?i)(password|passwd|secret|token|private[ _-]?key|credential)\s*[:=]`)
	storeIDSuffix     = `(?:[0-9a-f]{24}|[1-9][0-9]{18})`
	backupIDPattern   = regexp.MustCompile(`^backup_` + storeIDSuffix + `$`)
	instanceIDPattern = regexp.MustCompile(`^app_` + storeIDSuffix + `$`)
	serverIDPattern   = regexp.MustCompile(`^srv_` + storeIDSuffix + `$`)
	clusterIDPattern  = regexp.MustCompile(`^(?:mysql_cluster|cluster)_` + storeIDSuffix + `$`)
	taskIDPattern     = regexp.MustCompile(`^tsk_` + storeIDSuffix + `$`)
	uuidPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dnsLabelPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	numericIPv4Alias  = regexp.MustCompile(`(?i)^(?:0x[0-9a-f]+|[0-9]+)(?:\.(?:0x[0-9a-f]+|[0-9]+)){0,3}$`)
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
	if manifest.App != "mysql" {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	if manifest.Topology != "standalone" && manifest.Topology != "innodb-cluster" {
		return BackupManifest{}, mysqlOperationError(MySQLBackupUnsupportedTopology)
	}
	if !manifest.Consistent || manifest.CreatedAt.IsZero() ||
		!backupIDPattern.MatchString(manifest.BackupID) || !instanceIDPattern.MatchString(manifest.InstanceID) || !uuidPattern.MatchString(manifest.SourceServerUUID) || !taskIDPattern.MatchString(manifest.TaskID) ||
		!canonicalRequired(manifest.MySQLVersion, manifest.MySQLShellVersion) ||
		!serverIDPattern.MatchString(manifest.SourceServerID) {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	if containsSecretShape(manifest) {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	endpoint, ok := canonicalEndpoint(manifest.SourceEndpoint)
	if !ok {
		return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	manifest.SourceEndpoint = endpoint

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
		if manifest.ClusterID != "" || len(manifest.Members) != 0 {
			return BackupManifest{}, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
	case "innodb-cluster":
		if !clusterIDPattern.MatchString(manifest.ClusterID) {
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
	switch backupType {
	case "logical-full", "pre-restore":
	default:
		return mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	if targetTopology != normalized.Topology {
		return mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	if !canonicalRequired(targetMySQLVersion) || targetMySQLVersion != normalized.MySQLVersion {
		return mysqlOperationError(MySQLRestoreVersionIncompatible)
	}
	return nil
}

func normalizeBusinessSchemas(schemas []string) ([]string, error) {
	seen := make(map[string]struct{}, len(schemas))
	result := make([]string, 0, len(schemas))
	for _, raw := range schemas {
		schema := raw
		if !strictSchemaName.MatchString(schema) || isSystemSchema(schema) {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		folded := strings.ToLower(schema)
		if _, ok := seen[folded]; ok {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		seen[folded] = struct{}{}
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
		if strings.EqualFold(schema, systemSchema) {
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
		if _, ok := seen[schema]; ok || !isFixedSystemSchema(schema) {
			return false
		}
		seen[schema] = struct{}{}
	}
	return len(seen) == len(fixedSystemSchemas)
}

func isFixedSystemSchema(schema string) bool {
	for _, fixed := range fixedSystemSchemas {
		if schema == fixed {
			return true
		}
	}
	return false
}

func normalizeClusterMembers(members []ClusterMemberRef, sourceServerID, sourceEndpoint string) ([]ClusterMemberRef, error) {
	if len(members) != 3 {
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
		if !instanceIDPattern.MatchString(member.InstanceID) || !serverIDPattern.MatchString(member.ServerID) {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		endpoint, ok := canonicalEndpoint(member.Endpoint)
		if !ok {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		member.Endpoint = endpoint
		instanceKey := strings.ToLower(member.InstanceID)
		serverKey := strings.ToLower(member.ServerID)
		endpointKey := strings.ToLower(member.Endpoint)
		if _, ok := instances[instanceKey]; ok {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		if _, ok := servers[serverKey]; ok {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		if _, ok := endpoints[endpointKey]; ok {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		instances[instanceKey] = struct{}{}
		servers[serverKey] = struct{}{}
		endpoints[endpointKey] = struct{}{}
		if member.Status != "ONLINE" {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		if member.Role == "PRIMARY" {
			primaryCount++
			primary = *member
		} else if member.Role != "SECONDARY" {
			return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
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
	for index := range result {
		router := result[index]
		if !instanceIDPattern.MatchString(router.InstanceID) || !serverIDPattern.MatchString(router.ServerID) || !canonicalRequired(router.Status) {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		endpoint, ok := canonicalEndpoint(router.Endpoint)
		if !ok {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		result[index].Endpoint = endpoint
		key := strings.ToLower(router.InstanceID)
		if _, ok := seen[key]; ok {
			return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InstanceID == result[j].InstanceID {
			return result[i].ServerID < result[j].ServerID
		}
		return result[i].InstanceID < result[j].InstanceID
	})
	return result, nil
}

func canonicalEndpoint(endpoint string) (string, bool) {
	if !canonicalRequired(endpoint) || strings.Contains(endpoint, "@") || secretAssignment.MatchString(endpoint) {
		return "", false
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" {
		return "", false
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort <= 0 || parsedPort > 65535 || port != strconv.Itoa(parsedPort) {
		return "", false
	}
	if parsed := net.ParseIP(host); parsed != nil {
		host = parsed.String()
	} else {
		host = strings.ToLower(host)
		if strings.HasSuffix(host, ".") {
			host = strings.TrimSuffix(host, ".")
		}
		if host == "" || len(host) > 253 || strings.HasSuffix(host, ".") || numericIPv4Alias.MatchString(host) {
			return "", false
		}
		for _, label := range strings.Split(host, ".") {
			if !dnsLabelPattern.MatchString(label) {
				return "", false
			}
		}
	}
	return net.JoinHostPort(host, port), true
}

func containsSecretShape(value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return true
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return true
	}
	return containsSecretJSONShape(decoded)
}

func containsSecretJSONShape(value any) bool {
	switch typed := value.(type) {
	case string:
		value := strings.ToLower(strings.TrimSpace(typed))
		return isSecretJSONName(value) || secretAssignment.MatchString(typed) || strings.Contains(typed, "://") && strings.Contains(typed, "@")
	case []any:
		for _, item := range typed {
			if containsSecretJSONShape(item) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if isSecretJSONName(key) || containsSecretJSONShape(item) {
				return true
			}
		}
	}
	return false
}

func isSecretJSONName(value string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	return strings.Contains(normalized, "password") || strings.Contains(normalized, "passwd") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "token") || strings.Contains(normalized, "privatekey") || strings.Contains(normalized, "credential")
}

func canonicalRequired(values ...string) bool {
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return false
		}
	}
	return true
}

func canonicalIdentity(values ...string) bool {
	for _, value := range values {
		if !canonicalRequired(value) || value != strings.ToLower(value) {
			return false
		}
	}
	return true
}
