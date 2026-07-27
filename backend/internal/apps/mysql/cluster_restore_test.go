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

type clusterBackupFixture struct {
	*backupFakeStore
	instances []store.AppInstance
	servers   map[string]store.Server
	cluster   store.AppCluster
	members   []store.AppClusterMember
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

func healthyClusterRuntime(servers []store.Server) string {
	return "__AIFAR_CLUSTER__\t" + servers[0].Host + "\t3306\tPRIMARY\tONLINE\t123e4567-e89b-12d3-a456-426614174000\n" +
		"__AIFAR_CLUSTER__\t" + servers[1].Host + "\t3306\tSECONDARY\tONLINE\t223e4567-e89b-12d3-a456-426614174000\n" +
		"__AIFAR_CLUSTER__\t" + servers[2].Host + "\t3306\tSECONDARY\tONLINE\t323e4567-e89b-12d3-a456-426614174000\n"
}
