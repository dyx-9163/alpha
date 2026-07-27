package mysql

import (
	"context"
	"fmt"
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
