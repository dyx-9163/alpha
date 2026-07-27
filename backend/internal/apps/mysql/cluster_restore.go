package mysql

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

// Cluster operations intentionally resolve the PRIMARY at execution time. The
// database records identify the expected members but never select a source.
type clusterBackupStore interface {
	backupStore
	GetAppCluster(string) (store.AppCluster, error)
	ListAppClusterMembers(string) ([]store.AppClusterMember, error)
	ListAppInstances() ([]store.AppInstance, error)
}

type resolvedInnoDBCluster struct {
	clusterID      string
	members        []clusterMemberNode
	routers        []RouterRef
	primary        clusterMemberNode
	representative store.AppInstance
}

type clusterMemberNode struct {
	instance store.AppInstance
	server   store.Server
	endpoint string
	role     string
	status   string
	uuid     string
}

var clusterBackupStepNames = []string{
	"load-cluster", "acquire-cluster-lock", "resolve-members", "inspect-cluster", "resolve-primary",
	"backup-primary", "build-manifest", "record-backup", "apply-retention", "cleanup-workdir",
}

var healthyClusterRestoreStepNames = []string{
	"load-backup", "acquire-cluster-lock", "verify-maintenance-confirmation", "resolve-members", "inspect-cluster",
	"create-pre-restore-backup", "stop-application-writes", "upload-backup", "extract-backup", "dry-run-load",
	"capture-local-infile", "enable-local-infile", "drop-target-schemas", "load-primary", "restore-local-infile",
	"verify-primary", "verify-members", "verify-routers", "record-restore", "cleanup-workdir", "release-lock",
}

func (m Module) planInnoDBClusterBackup(ctx context.Context, req registry.BackupRequest) ([]registry.InstallStepPlan, error) {
	if err := validateClusterRequest(req.Instance, req.Instances, req.Servers); err != nil {
		return nil, err
	}
	return clusterBackupPlan(req.Instances, clusterBackupStepNames), nil
}

func (m Module) planHealthyClusterRestore(ctx context.Context, req registry.RestoreRequest) ([]registry.InstallStepPlan, error) {
	if err := validateClusterRequest(req.Instance, req.Instances, req.Servers); err != nil {
		return nil, err
	}
	if req.Backup.App != "mysql" || req.Backup.BackupType != "logical-full" || req.Backup.Status != "success" || strings.TrimSpace(req.RepositoryDir) == "" || clusterIDFromBackup(req.Backup) != clusterIDFromInstance(req.Instance) {
		return nil, mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	return clusterBackupPlan(req.Instances, healthyClusterRestoreStepNames), nil
}

func clusterBackupPlan(instances []store.AppInstance, names []string) []registry.InstallStepPlan {
	ordered := append([]store.AppInstance(nil), instances...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ServerID < ordered[j].ServerID })
	plan := make([]registry.InstallStepPlan, 0, len(ordered)*len(names))
	for _, instance := range ordered {
		for index, name := range names {
			plan = append(plan, registry.InstallStepPlan{Target: instance.ServerID, Name: name, Title: restoreStepTitle("en", name), Order: index + 1})
		}
	}
	return plan
}

func validateClusterRequest(representative store.AppInstance, instances []store.AppInstance, servers []store.Server) error {
	clusterID := clusterIDFromInstance(representative)
	if representative.App != "mysql" || instanceTopology(representative) != "innodb-cluster" || clusterID == "" || len(instances) != 3 || len(servers) != 3 {
		return mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	serverSet := map[string]store.Server{}
	for _, server := range servers {
		serverSet[server.ID] = server
	}
	seenInstances, seenServers := map[string]bool{}, map[string]bool{}
	matched := false
	for _, instance := range instances {
		if instance.App != "mysql" || instanceTopology(instance) != "innodb-cluster" || clusterIDFromInstance(instance) != clusterID || instance.ID == "" || instance.ServerID == "" || seenInstances[instance.ID] || seenServers[instance.ServerID] || serverSet[instance.ServerID].ID == "" {
			return mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		seenInstances[instance.ID], seenServers[instance.ServerID] = true, true
		matched = matched || instance.ID == representative.ID
	}
	if !matched {
		return mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	return nil
}

func clusterIDFromInstance(instance store.AppInstance) string {
	return strings.TrimSpace(metadataString(appMetadata(instance), "clusterId"))
}

func clusterIDFromBackup(backup store.AppBackup) string {
	metadata, err := strictBackupMetadata(backup.Metadata)
	if err != nil {
		return ""
	}
	var clusterID string
	_ = jsonRawString(metadata["clusterId"], &clusterID)
	return strings.TrimSpace(clusterID)
}

func jsonRawString(raw []byte, target *string) error {
	if len(raw) == 0 {
		return errors.New("missing JSON string")
	}
	return json.Unmarshal(raw, target)
}

func (s Service) resolveHealthyInnoDBCluster(ctx context.Context, representative store.AppInstance, taskID string) (resolvedInnoDBCluster, error) {
	data, ok := s.store.(clusterBackupStore)
	if !ok {
		return resolvedInnoDBCluster{}, errors.New("MySQL cluster backup store is unavailable")
	}
	clusterID := clusterIDFromInstance(representative)
	cluster, err := data.GetAppCluster(clusterID)
	if err != nil || cluster.App != "mysql" || normalizeTopology(cluster.Topology) != "innodb-cluster" {
		return resolvedInnoDBCluster{}, mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	members, err := data.ListAppClusterMembers(clusterID)
	if err != nil || len(members) != 3 {
		return resolvedInnoDBCluster{}, mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	instances, err := data.ListAppInstances()
	if err != nil {
		return resolvedInnoDBCluster{}, err
	}
	byID := map[string]store.AppInstance{}
	for _, instance := range instances {
		byID[instance.ID] = instance
	}
	result := resolvedInnoDBCluster{clusterID: clusterID, representative: representative}
	seenInstance, seenServer := map[string]bool{}, map[string]bool{}
	for _, member := range members {
		instance, found := byID[member.InstanceID]
		if !found || instance.App != "mysql" || instanceTopology(instance) != "innodb-cluster" || clusterIDFromInstance(instance) != clusterID || instance.ServerID != member.ServerID || seenInstance[instance.ID] || seenServer[instance.ServerID] {
			return resolvedInnoDBCluster{}, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		server, err := s.store.GetServer(instance.ServerID, false)
		if err != nil || strings.TrimSpace(server.Host) == "" {
			return resolvedInnoDBCluster{}, mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		seenInstance[instance.ID], seenServer[instance.ServerID] = true, true
		result.members = append(result.members, clusterMemberNode{instance: instance, server: server, endpoint: net.JoinHostPort(server.Host, strconv.Itoa(instancePort(instance)))})
	}
	foundRepresentative := false
	for _, member := range result.members {
		foundRepresentative = foundRepresentative || member.instance.ID == representative.ID
	}
	if !foundRepresentative {
		return resolvedInnoDBCluster{}, mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	for _, instance := range instances {
		if instance.App != "mysql-router" || clusterIDFromInstance(instance) != clusterID {
			continue
		}
		server, err := s.store.GetServer(instance.ServerID, false)
		if err != nil {
			return resolvedInnoDBCluster{}, err
		}
		result.routers = append(result.routers, RouterRef{InstanceID: instance.ID, ServerID: instance.ServerID, Endpoint: net.JoinHostPort(server.Host, strconv.Itoa(routerPort(instance))), Status: instance.Status})
	}
	if err := s.inspectInnoDBClusterRuntime(ctx, &result, taskID); err != nil {
		return resolvedInnoDBCluster{}, err
	}
	return result, nil
}

func (s Service) inspectInnoDBClusterRuntime(ctx context.Context, result *resolvedInnoDBCluster, taskID string) error {
	if result == nil || len(result.members) != 3 || !validLogicalTaskID(taskID) {
		return mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	probe := result.members[0]
	data, ok := s.store.(backupStore)
	if !ok {
		return errors.New("MySQL backup store is unavailable")
	}
	credential, err := data.GetBoundCredential(probe.instance.ID, "admin", true)
	if err != nil || credential.Status != "active" || credential.Kind != "mysql" || strings.TrimSpace(credential.Secret["password"]) == "" {
		return mysqlOperationError(MySQLCredentialUnavailable)
	}
	work := mysqlBackupWorkDir(taskID + "-cluster")
	if _, err := s.remote.Run(ctx, probe.server, bootstrapBackupWorkCommand(work)); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = s.remote.Run(cleanupCtx, probe.server, cleanupBackupCommand(work))
	}()
	secret, err := writeMySQLSecretContext(credential, instancePort(probe.instance))
	if err != nil {
		return mysqlOperationError(MySQLCredentialUnavailable)
	}
	defer os.Remove(secret)
	if err := s.remote.UploadFile(ctx, probe.server, secret, path.Join(work, "secret-context.cnf"), 0o600); err != nil {
		return err
	}
	output, err := s.remote.Run(ctx, probe.server, inspectClusterMembersCommand(work, instancePort(probe.instance)))
	if err != nil {
		return err
	}
	runtime, err := parseInnoDBClusterRuntime(output.Stdout)
	if err != nil {
		return mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	if len(runtime) != len(result.members) {
		return mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	byEndpoint := map[string]clusterMemberNode{}
	for _, member := range result.members {
		byEndpoint[normalizeEndpoint(member.endpoint)] = member
	}
	seenUUID, seenEndpoint, primaries := map[string]bool{}, map[string]bool{}, 0
	for _, observed := range runtime {
		endpoint := normalizeEndpoint(observed.endpoint)
		member, found := byEndpoint[endpoint]
		if !found || seenEndpoint[endpoint] || seenUUID[observed.uuid] || observed.status != "ONLINE" || (observed.role != "PRIMARY" && observed.role != "SECONDARY") {
			return mysqlOperationError(MySQLBackupClusterUnhealthy)
		}
		seenEndpoint[endpoint], seenUUID[observed.uuid] = true, true
		member.role, member.status, member.uuid = observed.role, observed.status, observed.uuid
		for index := range result.members {
			if result.members[index].instance.ID == member.instance.ID {
				result.members[index] = member
			}
		}
		if observed.role == "PRIMARY" {
			result.primary = member
			primaries++
		}
	}
	if primaries != 1 || result.primary.instance.ID == "" {
		return mysqlOperationError(MySQLBackupPrimaryNotFound)
	}
	return nil
}

type observedClusterMember struct{ endpoint, role, status, uuid string }

func inspectClusterMembersCommand(work string, port int) string {
	mysqlsh := path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh")
	query := "SELECT '__AIFAR_CLUSTER__',MEMBER_HOST,MEMBER_PORT,MEMBER_ROLE,MEMBER_STATE,MEMBER_ID FROM performance_schema.replication_group_members ORDER BY MEMBER_ID"
	return "set -eu; test -x " + installerkit.ShellQuote(mysqlsh) + "; " + installerkit.ShellQuote(mysqlsh) + " --defaults-file=" + installerkit.ShellQuote(path.Join(work, "secret-context.cnf")) + " --sql --raw --skip-column-names --host=127.0.0.1 --port=" + strconv.Itoa(port) + " --execute " + installerkit.ShellQuote(query)
}

func parseInnoDBClusterRuntime(output string) ([]observedClusterMember, error) {
	var out []observedClusterMember
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) != 6 || parts[0] != "__AIFAR_CLUSTER__" {
			continue
		}
		port, err := strconv.Atoi(parts[2])
		if err != nil || port < 1 || port > 65535 || !uuidPattern.MatchString(strings.ToLower(parts[5])) {
			return nil, errors.New("invalid cluster member")
		}
		out = append(out, observedClusterMember{endpoint: net.JoinHostPort(parts[1], parts[2]), role: parts[3], status: parts[4], uuid: strings.ToLower(parts[5])})
	}
	if err := scanner.Err(); err != nil || len(out) == 0 {
		return nil, errors.New("missing cluster member inspection")
	}
	return out, nil
}

func routerPort(instance store.AppInstance) int {
	if port := intParameter(appMetadata(instance), "readWritePort"); port > 0 {
		return port
	}
	return 6446
}

func (s Service) backupInnoDBCluster(ctx context.Context, req registry.BackupRequest, run registry.RunContext) error {
	cluster, err := s.resolveHealthyInnoDBCluster(ctx, req.Instance, run.TaskID)
	if err != nil {
		return err
	}
	members := make([]ClusterMemberRef, 0, len(cluster.members))
	ids := make([]string, 0, len(cluster.members))
	for _, member := range cluster.members {
		members = append(members, ClusterMemberRef{InstanceID: member.instance.ID, ServerID: member.server.ID, Endpoint: member.endpoint, Role: member.role, Status: member.status})
		ids = append(ids, member.instance.ID)
	}
	req.Instance, req.Instances, req.Servers = cluster.primary.instance, clusterInstances(cluster.members), clusterServers(cluster.members)
	return s.backupStandaloneCore(ctx, req, run, standaloneBackupExecution{backupType: "logical-full", recordPlan: true, retention: true, topology: "innodb-cluster", clusterID: cluster.clusterID, members: members, routers: cluster.routers, retentionInstanceIDs: ids})
}

func clusterInstances(members []clusterMemberNode) []store.AppInstance {
	out := make([]store.AppInstance, 0, len(members))
	for _, member := range members {
		out = append(out, member.instance)
	}
	return out
}
func clusterServers(members []clusterMemberNode) []store.Server {
	out := make([]store.Server, 0, len(members))
	for _, member := range members {
		out = append(out, member.server)
	}
	return out
}

func (m Module) restoreHealthyInnoDBCluster(ctx context.Context, req registry.RestoreRequest, run registry.RunContext) error {
	cluster, err := m.service.resolveHealthyInnoDBCluster(ctx, req.Instance, run.TaskID)
	if err != nil {
		return err
	}
	if clusterIDFromBackup(req.Backup) != cluster.clusterID {
		return mysqlOperationError(MySQLBackupClusterUnhealthy)
	}
	req.Instance, req.Instances, req.Servers = cluster.primary.instance, clusterInstances(cluster.members), clusterServers(cluster.members)
	return m.service.restoreLogical(ctx, req, run, &cluster)
}

func (s Service) verifyClusterRecovered(ctx context.Context, cluster *resolvedInnoDBCluster, taskID string) error {
	if err := s.inspectInnoDBClusterRuntime(ctx, cluster, taskID); err != nil {
		return err
	}
	if len(cluster.routers) == 0 {
		return nil
	}
	for _, router := range cluster.routers {
		server, err := s.store.GetServer(router.ServerID, false)
		if err != nil {
			return err
		}
		result, err := s.remote.Run(ctx, server, "set -eu; mysqlrouter --version >/dev/null; printf '__AIFAR_ROUTER__\\tOK\\n'")
		if err != nil || !strings.Contains(result.Stdout, "__AIFAR_ROUTER__\tOK") {
			return mysqlOperationError(MySQLRestoreIncomplete)
		}
	}
	return nil
}
