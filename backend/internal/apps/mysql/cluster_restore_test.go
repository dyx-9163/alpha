package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

// Production break caught: rejecting a healthy cluster at planning time would
// prevent the worker from resolving its execution-time PRIMARY.
func TestBackupInnoDBClusterPlanTargetsAllRecordedMembers(t *testing.T) {
	module := NewModule(newBackupFakeStore(t), newBackupFakeRemote())
	instances, servers := healthyClusterRequestFixtures()
	request := registry.BackupRequest{
		Instance: instances[1], Instances: instances, Servers: servers, RepositoryDir: t.TempDir(),
		Parameters: map[string]any{"threads": 4, "maxRateMBps": 0},
	}
	plan, err := module.PlanBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := planTargets(plan), []string{"cluster_1234567890abcdef12345678"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backup target = %v, want the single runtime-resolved cluster target %v", got, want)
	}
}

// Production break caught: choosing a stored role rather than the runtime
// ONLINE PRIMARY would send a dump to a replica or accept a split cluster.
func TestBackupInnoDBClusterUsesRuntimeOnlinePrimaryAndRecordsMembership(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	data := newClusterBackupFixture(t, instances, servers)
	remote := &clusterBackupRemote{backupFakeRemote: newBackupFakeRemote(), runtime: healthyClusterRuntime(servers)}
	module := NewModule(data, remote)
	request := registry.BackupRequest{Instance: instances[1], Instances: instances, Servers: servers, RepositoryDir: t.TempDir(), KeepLast: 2, Parameters: map[string]any{"threads": 4}}
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}}); err != nil {
		t.Fatal(err)
	}
	if remote.dumpRuns != 1 {
		t.Fatalf("dump runs = %d, want one PRIMARY dump", remote.dumpRuns)
	}
	if len(data.backups) != 1 || data.backups[0].ServerID != servers[0].ID || clusterIDFromBackup(data.backups[0]) != "cluster_1234567890abcdef12345678" {
		t.Fatalf("cluster backup record = %+v", data.backups)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(filepath.Dir(data.backups[0].Path), "backup-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Members) != 3 || manifest.Members[0].Role != "PRIMARY" || manifest.Members[0].Status != "ONLINE" || len(manifest.Routers) != 1 || manifest.Routers[0].Endpoint != "10.0.0.21:6446" || strings.Contains(strings.ToLower(string(manifestRaw)), "password") || strings.Contains(string(manifestRaw), "top-secret") {
		t.Fatalf("non-secret runtime membership manifest = %s", manifestRaw)
	}
}

// Production break caught: accepting any non-ONLINE, split, incomplete, or
// duplicate-UUID runtime topology can dump a divergent member.
func TestBackupInnoDBClusterRejectsUnhealthyRuntimeBeforeDump(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	baseRuntime := healthyClusterRuntime(servers)
	tests := []struct {
		name    string
		runtime string
		mutate  func(*clusterBackupFixture, *registry.BackupRequest)
		code    string
	}{
		{"recovering", strings.Replace(baseRuntime, "SECONDARY\tONLINE", "SECONDARY\tRECOVERING", 1), nil, MySQLBackupClusterUnhealthy},
		{"offline", strings.Replace(baseRuntime, "SECONDARY\tONLINE", "SECONDARY\tOFFLINE", 1), nil, MySQLBackupClusterUnhealthy},
		{"error", strings.Replace(baseRuntime, "SECONDARY\tONLINE", "SECONDARY\tERROR", 1), nil, MySQLBackupClusterUnhealthy},
		{"missing primary", strings.Replace(baseRuntime, "PRIMARY\tONLINE", "SECONDARY\tONLINE", 1), nil, MySQLBackupPrimaryNotFound},
		{"split primary", strings.Replace(baseRuntime, "SECONDARY\tONLINE", "PRIMARY\tONLINE", 1), nil, MySQLBackupPrimaryNotFound},
		{"incomplete runtime", strings.Split(baseRuntime, "\n")[0] + "\n" + strings.Split(baseRuntime, "\n")[1] + "\n", nil, MySQLBackupClusterUnhealthy},
		{"duplicate uuid", strings.Replace(baseRuntime, "223e4567-e89b-12d3-a456-426614174000", "123e4567-e89b-12d3-a456-426614174000", 1), nil, MySQLBackupClusterUnhealthy},
		{"incomplete persisted", baseRuntime, func(data *clusterBackupFixture, _ *registry.BackupRequest) { data.members = data.members[:2] }, MySQLBackupClusterUnhealthy},
		{"representative outside cluster", baseRuntime, func(_ *clusterBackupFixture, request *registry.BackupRequest) {
			request.Instance = store.AppInstance{ID: "app_999999999999999999999999", App: "mysql", ServerID: "srv_999999999999999999999999", Topology: "innodb-cluster", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678"}`}
		}, MySQLBackupClusterUnhealthy},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := newClusterBackupFixture(t, instances, servers)
			remote := &clusterBackupRemote{backupFakeRemote: newBackupFakeRemote(), runtime: test.runtime}
			request := registry.BackupRequest{Instance: instances[1], Instances: instances, Servers: servers, RepositoryDir: t.TempDir(), KeepLast: 2, Parameters: map[string]any{"threads": 4}}
			if test.mutate != nil {
				test.mutate(data, &request)
			}
			err := NewModule(data, remote).Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
			var operation *MySQLOperationError
			if !errors.As(err, &operation) || operation.Code != test.code || remote.dumpRuns != 0 {
				t.Fatalf("err=%v code=%v dumps=%d", err, operation, remote.dumpRuns)
			}
		})
	}
}

// Production break caught: replacing the 6446 transaction with a version
// probe or a read-only statement would no longer prove Router write routing.
func TestRouterReadWriteVerificationUsesTemporaryWriteAndRead(t *testing.T) {
	command := routerReadWriteVerificationCommand("/aifar/apps/mysql/_backup/task-router", 6446)
	for _, want := range []string{"CREATE TEMPORARY TABLE", "INSERT INTO aifar_router_verify", "__AIFAR_ROUTER_WRITE__", "__AIFAR_ROUTER_READ__", "--port=6446", "secret-context.cnf"} {
		if !strings.Contains(command, want) {
			t.Fatalf("router verification missing %q: %s", want, command)
		}
	}
	if strings.Contains(command, "top-secret") || strings.Contains(command, "--password") {
		t.Fatalf("router verification exposes a secret: %s", command)
	}
}

// Production break caught: a healthy cluster restore must remain a cluster
// operation; accepting the request as a standalone target would permit a stale
// member to be selected before runtime PRIMARY discovery.
func TestRestoreHealthyClusterPlanTargetsAllRecordedMembers(t *testing.T) {
	module := NewModule(newBackupFakeStore(t), newBackupFakeRemote())
	instances, servers := healthyClusterRequestFixtures()
	request := registry.RestoreRequest{
		Instance: instances[2], Instances: instances, Servers: servers,
		Backup:        store.AppBackup{ID: "backup_1234567890abcdef12345678", App: "mysql", InstanceID: instances[0].ID, ServerID: servers[0].ID, BackupType: "logical-full", Status: "success", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678"}`},
		RepositoryDir: t.TempDir(), Parameters: map[string]any{"mode": "innodb-cluster", "maintenanceConfirmed": true, "createPreRestoreBackup": true, "threads": 4},
	}
	plan, err := module.PlanRestore(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := planTargets(plan), []string{"cluster_1234567890abcdef12345678"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("restore target = %v, want the single runtime-resolved cluster target %v", got, want)
	}
	if got := planStepNames(plan); !reflect.DeepEqual(got, standaloneRestoreStepNames) {
		t.Fatalf("restore plan steps = %v, want the terminal lifecycle %v", got, standaloneRestoreStepNames)
	}
}

// Production break caught: generating per-member copies of PRIMARY-only work
// leaves task targets and step terminals that the worker can never close.
func TestClusterPlansHaveOneTerminalExecutionTarget(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	module := NewModule(newBackupFakeStore(t), newBackupFakeRemote())
	plan, err := module.PlanBackup(context.Background(), registry.BackupRequest{Instance: instances[0], Instances: instances, Servers: servers, RepositoryDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != len(clusterBackupStepNames) || len(planTargets(plan)) != 1 || planTargets(plan)[0] != "cluster_1234567890abcdef12345678" {
		t.Fatalf("cluster plan = %+v", plan)
	}
}

// Production break caught: resolving PRIMARY only once would allow a failover
// during staging to drop schemas on the formerly selected member.
func TestClusterRestoreRejectsPrimaryFailoverBeforeSchemaMutation(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	data, request := newClusterRestoreFixture(t, instances, servers)
	remote := &clusterRestoreRemote{restoreFakeRemote: &restoreFakeRemote{inspect: standaloneInspection("aifar_business")}, runtimes: []string{
		healthyClusterRuntime(servers), runtimeWithPrimary(servers, 1),
	}}
	module := configuredClusterRestoreModule(data, remote)
	recorder := &restoreProgressRecorder{}
	err := module.Restore(context.Background(), request, registry.RunContext{TaskID: "task_cluster_failover", Log: recorder})
	assertClusterRestoreFailure(t, err, MySQLRestorePrimaryChanged, data, request, remote, recorder)
	if containsRestoreCommand(remote.commands, "DROP DATABASE") || containsRestoreCommand(remote.commands, "logical-restore.sh") {
		t.Fatalf("failover reached destructive restore commands: %v", remote.commands)
	}
}

// Production break caught: treating a successful load as recovery without a
// fresh all-member ONLINE check could publish a partially restored cluster.
func TestClusterRestoreMarksIncompleteWhenMemberLeavesOnlineAfterLoad(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	data, request := newClusterRestoreFixture(t, instances, servers)
	unhealthy := strings.Replace(healthyClusterRuntime(servers), "SECONDARY\tONLINE", "SECONDARY\tRECOVERING", 1)
	remote := &clusterRestoreRemote{restoreFakeRemote: &restoreFakeRemote{inspect: standaloneInspection("aifar_business")}, runtimes: []string{
		healthyClusterRuntime(servers), healthyClusterRuntime(servers), unhealthy,
	}}
	module := configuredClusterRestoreModule(data, remote)
	recorder := &restoreProgressRecorder{}
	err := module.Restore(context.Background(), request, registry.RunContext{TaskID: "task_cluster_member_failed", Log: recorder})
	assertClusterRestoreFailure(t, err, MySQLRestoreIncomplete, data, request, remote, recorder)
	assertOneLogicalLoadWithBinlogEnabled(t, remote)
}

// Production break caught: a Router connection or read-only probe can succeed
// while write routing is broken; the post-load transaction must gate success.
func TestClusterRestoreMarksIncompleteWhenRouterTransactionFails(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	data, request := newClusterRestoreFixture(t, instances, servers)
	remote := &clusterRestoreRemote{
		restoreFakeRemote: &restoreFakeRemote{inspect: standaloneInspection("aifar_business")},
		runtimes:          []string{healthyClusterRuntime(servers), healthyClusterRuntime(servers), healthyClusterRuntime(servers)},
		routerErr:         errors.New("router transaction failed"),
	}
	module := configuredClusterRestoreModule(data, remote)
	recorder := &restoreProgressRecorder{}
	err := module.Restore(context.Background(), request, registry.RunContext{TaskID: "task_cluster_router_failed", Log: recorder})
	assertClusterRestoreFailure(t, err, MySQLRestoreIncomplete, data, request, remote, recorder)
	assertOneLogicalLoadWithBinlogEnabled(t, remote)
}

func assertClusterRestoreFailure(t *testing.T, err error, code string, data *clusterRestoreFixture, request registry.RestoreRequest, remote *clusterRestoreRemote, recorder *restoreProgressRecorder) {
	t.Helper()
	var operation *MySQLOperationError
	if !errors.As(err, &operation) || operation.Code != code {
		t.Fatalf("restore error = %v, want %s", err, code)
	}
	if got := restorePhase(data.backups[0].Metadata); got != "restore_incomplete" {
		t.Fatalf("restore phase = %q, want restore_incomplete", got)
	}
	if !boolParameter(request.Parameters, "maintenanceConfirmed") {
		t.Fatal("failed restore cleared the caller's maintenance confirmation")
	}
	if len(recorder.targets) != 1 || recorder.targets[0] != "cluster_1234567890abcdef12345678" || len(recorder.finishedTargets) != 1 || recorder.targetFinishes[0] != "failed" {
		t.Fatalf("cluster target lifecycle targets=%v finished=%v status=%v", recorder.targets, recorder.finishedTargets, recorder.targetFinishes)
	}
	if len(recorder.stepStatus) != len(standaloneRestoreStepNames) {
		t.Fatalf("terminal steps=%d statuses=%v", len(recorder.stepStatus), recorder.stepStatus)
	}
	for _, name := range standaloneRestoreStepNames {
		if status := recorder.stepStatus[name]; status != "success" && status != "failed" && status != "cancelled" {
			t.Fatalf("step %s status=%q", name, status)
		}
	}
}

func assertOneLogicalLoadWithBinlogEnabled(t *testing.T, remote *clusterRestoreRemote) {
	t.Helper()
	loads := 0
	for _, command := range remote.commands {
		if strings.Contains(command, "logical-restore.sh") {
			loads++
		}
	}
	if loads != 1 {
		t.Fatalf("logical restore executions = %d, want one without automatic pre-restore rollback: %v", loads, remote.commands)
	}
	var script string
	for path, content := range remote.uploads {
		if strings.HasSuffix(path, "/logical-restore.sh") {
			script = content
		}
	}
	if !strings.Contains(script, "skipBinlog: false") {
		t.Fatalf("logical restore script does not preserve binlog replication: %q", script)
	}
}

func containsRestoreCommand(commands []string, fragment string) bool {
	for _, command := range commands {
		if strings.Contains(command, fragment) {
			return true
		}
	}
	return false
}

func healthyClusterRequestFixtures() ([]store.AppInstance, []store.Server) {
	clusterID := "cluster_1234567890abcdef12345678"
	servers := []store.Server{
		{ID: "srv_111111111111111111111111", Host: "10.0.0.11", Username: "root"},
		{ID: "srv_222222222222222222222222", Host: "10.0.0.12", Username: "root"},
		{ID: "srv_333333333333333333333333", Host: "10.0.0.13", Username: "root"},
	}
	instances := []store.AppInstance{
		{ID: "app_111111111111111111111111", App: "mysql", ServerID: servers[0].ID, Version: "8.0.36", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `","port":3306,"endpoint":"10.0.0.11:3306"}`},
		{ID: "app_222222222222222222222222", App: "mysql", ServerID: servers[1].ID, Version: "8.0.36", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `","port":3306,"endpoint":"10.0.0.12:3306"}`},
		{ID: "app_333333333333333333333333", App: "mysql", ServerID: servers[2].ID, Version: "8.0.36", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `","port":3306,"endpoint":"10.0.0.13:3306"}`},
	}
	return instances, servers
}

func planTargets(plan []registry.InstallStepPlan) []string {
	seen := map[string]bool{}
	var targets []string
	for _, step := range plan {
		if !seen[step.Target] {
			seen[step.Target] = true
			targets = append(targets, step.Target)
		}
	}
	return targets
}

func planStepNames(plan []registry.InstallStepPlan) []string {
	names := make([]string, 0, len(plan))
	for _, step := range plan {
		names = append(names, step.Name)
	}
	return names
}

type clusterBackupFixture struct {
	*backupFakeStore
	instances []store.AppInstance
	servers   map[string]store.Server
	cluster   store.AppCluster
	members   []store.AppClusterMember
}

type clusterRestoreFixture struct {
	*restoreFakeStore
	instances []store.AppInstance
	servers   map[string]store.Server
	cluster   store.AppCluster
	members   []store.AppClusterMember
}

func newClusterRestoreFixture(t *testing.T, instances []store.AppInstance, servers []store.Server) (*clusterRestoreFixture, registry.RestoreRequest) {
	t.Helper()
	base := &restoreFakeStore{backupFakeStore: newBackupFakeStore(t)}
	base.instance, base.server = instances[0], servers[0]
	byServer := map[string]store.Server{}
	members := make([]store.AppClusterMember, 0, len(instances))
	for index, server := range servers {
		byServer[server.ID] = server
		members = append(members, store.AppClusterMember{ClusterID: "cluster_1234567890abcdef12345678", InstanceID: instances[index].ID, ServerID: server.ID, Role: "SECONDARY", Status: "active"})
	}
	routerServer := store.Server{ID: "srv_444444444444444444444444", Host: "10.0.0.21", Username: "root"}
	byServer[routerServer.ID] = routerServer
	allInstances := append(append([]store.AppInstance(nil), instances...), store.AppInstance{ID: "app_444444444444444444444444", App: "mysql-router", ServerID: routerServer.ID, Version: "8.0.36", Topology: "router", Status: "installed", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678","readWritePort":6446}`})
	repositoryDir, backup := createClusterRestoreBackup(t, instances, servers)
	base.backups = []store.AppBackup{backup}
	data := &clusterRestoreFixture{
		restoreFakeStore: base, instances: allInstances, servers: byServer,
		cluster: store.AppCluster{ID: "cluster_1234567890abcdef12345678", App: "mysql", Topology: "innodb-cluster", Status: "active"}, members: members,
	}
	return data, registry.RestoreRequest{
		Instance: instances[1], Instances: append([]store.AppInstance(nil), instances...), Servers: append([]store.Server(nil), servers...),
		Backup: backup, RepositoryDir: repositoryDir, Language: "en", Actor: "owner",
		Parameters: map[string]any{"mode": "innodb-cluster", "maintenanceConfirmed": true, "createPreRestoreBackup": true, "disasterConfirmed": false, "threads": 4},
	}
}

func (s *clusterRestoreFixture) GetServer(id string, _ bool) (store.Server, error) {
	server, ok := s.servers[id]
	if !ok {
		return store.Server{}, fmt.Errorf("server %s not found", id)
	}
	return server, nil
}

func (s *clusterRestoreFixture) ListAppInstances() ([]store.AppInstance, error) {
	return append([]store.AppInstance(nil), s.instances...), nil
}

func (s *clusterRestoreFixture) GetAppCluster(id string) (store.AppCluster, error) {
	if id != s.cluster.ID {
		return store.AppCluster{}, fmt.Errorf("cluster not found")
	}
	return s.cluster, nil
}

func (s *clusterRestoreFixture) ListAppClusterMembers(id string) ([]store.AppClusterMember, error) {
	if id != s.cluster.ID {
		return nil, fmt.Errorf("members not found")
	}
	return append([]store.AppClusterMember(nil), s.members...), nil
}

func createClusterRestoreBackup(t *testing.T, instances []store.AppInstance, servers []store.Server) (string, store.AppBackup) {
	t.Helper()
	repositoryDir, backup := createStandaloneRestoreBackup(t, instances[0])
	manifestPath := filepath.Join(filepath.Dir(backup.Path), "backup-manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeRestoreManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Topology = "innodb-cluster"
	manifest.ClusterID = "cluster_1234567890abcdef12345678"
	manifest.SourceEndpoint = servers[0].Host + ":3306"
	manifest.Members = make([]ClusterMemberRef, 0, len(instances))
	for index, instance := range instances {
		role := "SECONDARY"
		if index == 0 {
			role = "PRIMARY"
		}
		manifest.Members = append(manifest.Members, ClusterMemberRef{InstanceID: instance.ID, ServerID: instance.ServerID, Endpoint: servers[index].Host + ":3306", Role: role, Status: "ONLINE"})
	}
	manifest.Routers = []RouterRef{{InstanceID: "app_444444444444444444444444", ServerID: "srv_444444444444444444444444", Endpoint: "10.0.0.21:6446", Status: "installed"}}
	canonical, err := CanonicalBackupManifestJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	backup.Metadata = `{"clusterId":"cluster_1234567890abcdef12345678"}`
	return repositoryDir, backup
}

func configuredClusterRestoreModule(data *clusterRestoreFixture, remote *clusterRestoreRemote) Module {
	module := NewModule(data, remote)
	module.service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
	module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
	}
	return module
}

func newClusterBackupFixture(t *testing.T, instances []store.AppInstance, servers []store.Server) *clusterBackupFixture {
	t.Helper()
	base := newBackupFakeStore(t)
	base.instance, base.server = instances[0], servers[0]
	byServer := map[string]store.Server{}
	members := make([]store.AppClusterMember, 0, len(instances))
	for index, server := range servers {
		byServer[server.ID] = server
		members = append(members, store.AppClusterMember{ClusterID: "cluster_1234567890abcdef12345678", InstanceID: instances[index].ID, ServerID: server.ID, Role: "SECONDARY", Status: "active"})
	}
	routerServer := store.Server{ID: "srv_444444444444444444444444", Host: "10.0.0.21", Username: "root"}
	byServer[routerServer.ID] = routerServer
	instances = append(instances, store.AppInstance{ID: "app_444444444444444444444444", App: "mysql-router", ServerID: routerServer.ID, Version: "8.0.36", Topology: "router", Status: "installed", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678","readWritePort":6446}`})
	return &clusterBackupFixture{backupFakeStore: base, instances: instances, servers: byServer, cluster: store.AppCluster{ID: "cluster_1234567890abcdef12345678", App: "mysql", Topology: "innodb-cluster", Status: "active"}, members: members}
}

func (s *clusterBackupFixture) GetServer(id string, _ bool) (store.Server, error) {
	server, ok := s.servers[id]
	if !ok {
		return store.Server{}, fmt.Errorf("server %s not found", id)
	}
	return server, nil
}
func (s *clusterBackupFixture) ListAppInstances() ([]store.AppInstance, error) {
	return append([]store.AppInstance(nil), s.instances...), nil
}
func (s *clusterBackupFixture) GetAppCluster(id string) (store.AppCluster, error) {
	if id != s.cluster.ID {
		return store.AppCluster{}, fmt.Errorf("cluster not found")
	}
	return s.cluster, nil
}
func (s *clusterBackupFixture) ListAppClusterMembers(id string) ([]store.AppClusterMember, error) {
	if id != s.cluster.ID {
		return nil, fmt.Errorf("members not found")
	}
	return append([]store.AppClusterMember(nil), s.members...), nil
}

type clusterBackupRemote struct {
	*backupFakeRemote
	runtime string
}

func (r *clusterBackupRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if strings.Contains(command, "__AIFAR_CLUSTER__") {
		return adapter.CommandResult{Stdout: r.runtime}, nil
	}
	return r.backupFakeRemote.Run(ctx, server, command)
}

type clusterRestoreRemote struct {
	*restoreFakeRemote
	runtimes     []string
	runtimeCalls int
	routerErr    error
	routerOutput string
}

func (r *clusterRestoreRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if strings.Contains(command, "__AIFAR_CLUSTER__") {
		r.commands = append(r.commands, command)
		index := r.runtimeCalls
		r.runtimeCalls++
		if index >= len(r.runtimes) {
			index = len(r.runtimes) - 1
		}
		if index < 0 {
			return adapter.CommandResult{}, errors.New("missing cluster runtime fixture")
		}
		return adapter.CommandResult{Stdout: r.runtimes[index]}, nil
	}
	if strings.Contains(command, "__AIFAR_ROUTER_WRITE__") {
		r.commands = append(r.commands, command)
		output := r.routerOutput
		if output == "" {
			output = "__AIFAR_ROUTER_WRITE__\t1\n__AIFAR_ROUTER_READ__\t1\n"
		}
		return adapter.CommandResult{Stdout: output}, r.routerErr
	}
	return r.restoreFakeRemote.Run(ctx, server, command)
}

func healthyClusterRuntime(servers []store.Server) string {
	return runtimeWithPrimary(servers, 0)
}

func runtimeWithPrimary(servers []store.Server, primary int) string {
	uuids := []string{
		"123e4567-e89b-12d3-a456-426614174000",
		"223e4567-e89b-12d3-a456-426614174000",
		"323e4567-e89b-12d3-a456-426614174000",
	}
	var output strings.Builder
	for index, server := range servers {
		role := "SECONDARY"
		if index == primary {
			role = "PRIMARY"
		}
		output.WriteString("__AIFAR_CLUSTER__\t" + server.Host + "\t3306\t" + role + "\tONLINE\t" + uuids[index] + "\n")
	}
	return output.String()
}
