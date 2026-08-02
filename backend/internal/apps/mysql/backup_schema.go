package mysql

import (
	"context"
	"sort"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

var mysqlServerSystemSchemas = []string{"information_schema", "mysql", "ndbinfo", "performance_schema", "sys"}

type mysqlBackupSchema struct {
	Name           string
	EstimatedBytes int64
}

func isServerSystemSchema(name string) bool {
	for _, system := range mysqlServerSystemSchemas {
		if strings.EqualFold(name, system) {
			return true
		}
	}
	return false
}

func isClusterMetadataSchema(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "mysql_innodb_cluster_metadata")
}

func backupSchemaCategory(name string) registry.BackupSchemaCategory {
	if isServerSystemSchema(name) {
		return registry.BackupSchemaServerSystem
	}
	if isClusterMetadataSchema(name) {
		return registry.BackupSchemaClusterMetadata
	}
	return registry.BackupSchemaBusiness
}

func classifyBackupSchemas(available []mysqlBackupSchema) ([]registry.BackupSchema, error) {
	seen := make(map[string]bool, len(available))
	result := make([]registry.BackupSchema, 0, len(available))
	for _, candidate := range available {
		if !strictSchemaName.MatchString(candidate.Name) || candidate.EstimatedBytes < 0 || seen[strings.ToLower(candidate.Name)] {
			return nil, mysqlOperationError(MySQLBackupSchemaSelectionInvalid)
		}
		seen[strings.ToLower(candidate.Name)] = true
		category := backupSchemaCategory(candidate.Name)
		selectable := category == registry.BackupSchemaBusiness
		result = append(result, registry.BackupSchema{Name: candidate.Name, Category: category, Selectable: selectable, SelectedByDefault: selectable})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func selectedSchemaParameter(parameters map[string]any) ([]string, bool) {
	if parameters == nil {
		return nil, false
	}
	raw, present := parameters["schemas"]
	if !present {
		return nil, false
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...), true
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, true
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, true
	}
}

func selectBackupSchemas(available []mysqlBackupSchema, requested []string) ([]string, []string, int64, error) {
	if len(requested) == 0 {
		return nil, nil, 0, mysqlOperationError(MySQLBackupSchemaSelectionInvalid)
	}
	byName := make(map[string]mysqlBackupSchema, len(available))
	for _, candidate := range available {
		key := strings.ToLower(candidate.Name)
		if !strictSchemaName.MatchString(candidate.Name) || candidate.EstimatedBytes < 0 || byName[key].Name != "" {
			return nil, nil, 0, mysqlOperationError(MySQLBackupSchemaSelectionInvalid)
		}
		byName[key] = candidate
	}
	selectedSet := make(map[string]bool, len(requested))
	selected := make([]string, 0, len(requested))
	var estimated int64
	for _, raw := range requested {
		if !strictSchemaName.MatchString(raw) {
			return nil, nil, 0, mysqlOperationError(MySQLBackupSchemaSelectionInvalid)
		}
		key := strings.ToLower(raw)
		candidate, found := byName[key]
		if !found || selectedSet[key] || backupSchemaCategory(candidate.Name) != registry.BackupSchemaBusiness {
			return nil, nil, 0, mysqlOperationError(MySQLBackupSchemaSelectionInvalid)
		}
		selectedSet[key] = true
		selected = append(selected, candidate.Name)
		if candidate.EstimatedBytes > 0 && estimated > int64(^uint64(0)>>1)-candidate.EstimatedBytes {
			return nil, nil, 0, mysqlOperationError(MySQLBackupSchemaSelectionInvalid)
		}
		estimated += candidate.EstimatedBytes
	}
	sort.Strings(selected)
	excludedSet := make(map[string]string)
	for _, fixed := range fixedSystemSchemas {
		excludedSet[strings.ToLower(fixed)] = fixed
	}
	for _, candidate := range available {
		if !selectedSet[strings.ToLower(candidate.Name)] {
			excludedSet[strings.ToLower(candidate.Name)] = candidate.Name
		}
	}
	excluded := make([]string, 0, len(excludedSet))
	for _, name := range excludedSet {
		excluded = append(excluded, name)
	}
	sort.Strings(excluded)
	return selected, excluded, estimated, nil
}

func (m Module) DiscoverBackupSchemas(ctx context.Context, req registry.BackupRequest) (registry.BackupSchemaCatalog, error) {
	if req.Instance.App != "mysql" {
		return registry.BackupSchemaCatalog{}, mysqlOperationError(MySQLBackupUnsupportedTopology)
	}
	return m.service.discoverBackupSchemas(ctx, req)
}

func (s Service) discoverBackupSchemas(ctx context.Context, req registry.BackupRequest) (registry.BackupSchemaCatalog, error) {
	taskID := store.NewID("tsk")
	instance := req.Instance
	if instanceTopology(instance) == "innodb-cluster" {
		cluster, err := s.resolveHealthyInnoDBCluster(ctx, instance, taskID)
		if err != nil {
			return registry.BackupSchemaCatalog{}, err
		}
		instance = cluster.primary.instance
	} else if instanceTopology(instance) != "standalone" {
		return registry.BackupSchemaCatalog{}, mysqlOperationError(MySQLBackupUnsupportedTopology)
	}
	if err := s.requireNoMySQLMaintenance(instance, req.Language); err != nil {
		return registry.BackupSchemaCatalog{}, err
	}
	if err := s.requireNoMySQLReconciliation(instance, req.Language); err != nil {
		return registry.BackupSchemaCatalog{}, err
	}
	server, err := s.store.GetServer(instance.ServerID, true)
	if err != nil {
		return registry.BackupSchemaCatalog{}, err
	}
	credential, err := resolveMySQLAdminCredential(s.store, instance)
	if err != nil {
		return registry.BackupSchemaCatalog{}, err
	}
	work := mysqlBackupWorkDir(taskID + "-schemas")
	var inspection mysqlBackupInspection
	err = s.withMySQLCredentialWork(ctx, server, work, credential, instancePort(instance), func() error {
		result, runErr := s.remote.Run(ctx, server, inspectBackupCommand(work, instancePort(instance)))
		if runErr != nil {
			return runErr
		}
		inspection, runErr = parseMySQLBackupInspection(result.Stdout)
		return runErr
	})
	if err != nil {
		return registry.BackupSchemaCatalog{}, err
	}
	items, err := classifyBackupSchemas(inspection.AvailableSchemas)
	if err != nil {
		return registry.BackupSchemaCatalog{}, err
	}
	return registry.BackupSchemaCatalog{InstanceID: req.Instance.ID, SourceInstanceID: instance.ID, SourceServerID: server.ID, Schemas: items}, nil
}

var _ registry.BackupSchemaModule = Module{}
