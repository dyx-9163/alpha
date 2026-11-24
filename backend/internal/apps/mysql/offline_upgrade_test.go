package mysql

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestOfflineUpgradeRejectsForeignGroupView(t *testing.T) {
	views := upgradeTestViews()
	views[2].group = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	if _, err := validateUpgradeViews(views, map[int]string{0: "8.0.36", 1: "8.0.36", 2: "8.0.36"}, nil); err == nil {
		t.Fatal("accepted three SQL connections reporting different group UUIDs")
	}
}

func TestOfflineUpgradeOrderUsesLivePrimary(t *testing.T) {
	views := upgradeTestViews()
	primary, err := validateUpgradeViews(views, map[int]string{0: "8.0.36", 1: "8.0.36", 2: "8.0.36"}, nil)
	if err != nil || primary != 1 {
		t.Fatalf("live primary = %d, error = %v", primary, err)
	}
}

func TestOfflineUpgradeRejectsNodeReturningWithDifferentIdentity(t *testing.T) {
	views := upgradeTestViews()
	baseline := []string{views[0].uuid, views[1].uuid, "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"}
	if _, err := validateUpgradeViews(views, map[int]string{0: "8.0.36", 1: "8.0.36", 2: "8.0.36"}, baseline); err == nil {
		t.Fatal("accepted a replacement member UUID")
	}
}

func upgradeTestViews() []upgradeView {
	uuids := []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}
	group := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	members := []upgradeMember{
		{uuid: uuids[0], role: "SECONDARY", state: "ONLINE", version: "8.0.36", endpoint: "10.0.0.1:3306"},
		{uuid: uuids[1], role: "PRIMARY", state: "ONLINE", version: "8.0.36", endpoint: "10.0.0.2:3306"},
		{uuid: uuids[2], role: "SECONDARY", state: "ONLINE", version: "8.0.36", endpoint: "10.0.0.3:3306"},
	}
	var views []upgradeView
	for index, uuid := range uuids {
		views = append(views, upgradeView{uuid: uuid, group: group, version: "8.0.36", gtid: uuid + ":1-10", endpoint: members[index].endpoint, members: members})
	}
	return views
}

func TestOfflineUpgradeCandidateKeepsInPlacePathsAndRemovedOptionHandling(t *testing.T) {
	for _, required := range []string{"binlog[-_]transaction[-_]dependency[-_]tracking", "--validate-config", "mysql_native_password", "stop_cleanly", "'__AIFAR_UPGRADE_LOCAL__' AS marker", "'__AIFAR_UPGRADE_MEMBER__' AS marker"} {
		if !strings.Contains(offlineUpgradeScript, required) {
			t.Fatalf("candidate and startup path lost %s", required)
		}
	}
	for _, forbidden := range []string{"--initialize", "group_replication_force_members", "START GROUP_REPLICATION"} {
		if strings.Contains(offlineUpgradeScript, forbidden) {
			t.Fatalf("upgrade introduced forbidden command %s", forbidden)
		}
	}
}

func TestInstallTemplateKeepsWriteSetOptionOnlyForSourceVersion(t *testing.T) {
	for _, tt := range []struct {
		version string
		want    bool
	}{{"8.0.36", true}, {"8.4.13", false}} {
		script, err := installStandaloneScript(InstallScriptRequest{Version: tt.version})
		if err != nil {
			t.Fatal(err)
		}
		const option = "binlog_transaction_dependency_tracking=WRITESET"
		if present := strings.Contains(script, option); present != tt.want {
			t.Errorf("MySQL %s rendered option %s = %t, want %t", tt.version, option, present, tt.want)
		}
	}
}

func TestOfflineUpgradeRollsLivePrimaryLastAndRecordsOnlyVerifiedVersions(t *testing.T) {
	data, remote, resources, run := newOfflineUpgradeFixture(t)
	service := NewService(data, remote)
	if err := service.RunOfflineUpgrade(context.Background(), data.instances[0], resources, run); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(remote.rolled, []int{0, 2, 1}) || remote.firstRollBeforeTools {
		t.Fatalf("unexpected rollout order or premature server switch: %v", remote.rolled)
	}
	if len(data.updated) != 4 {
		t.Fatalf("recorded %d versions, expected three MySQL Server records and one Router record", len(data.updated))
	}
	for _, item := range data.updated {
		if item.Version != "8.4.13" || !strings.Contains(item.Metadata, "offlineUpgradeBackupPath") {
			t.Fatalf("version and recovery projection missing: %v", item)
		}
	}
}

func TestOfflineUpgradeStopsBeforeFurtherServersWhenCutoverFails(t *testing.T) {
	data, remote, resources, run := newOfflineUpgradeFixture(t)
	remote.failServer = 2
	service := NewService(data, remote)
	if err := service.RunOfflineUpgrade(context.Background(), data.instances[0], resources, run); err == nil {
		t.Fatal("failed server switch returned success")
	}
	if !slices.Equal(remote.rolled, []int{0}) || data.updated[data.instances[0].ID].Version != "8.4.13" || data.updated[data.instances[1].ID].Version != "" {
		t.Fatalf("partial versions or rollover sequence are incorrect: rolled=%v, updated=%v", remote.rolled, data.updated)
	}
	logs := strings.Join(run.Log.(*backupRecorder).messages, "\n")
	if strings.Contains(logs, "upgrade-test-password") || !strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("command errors must redact MySQL passwords: %s", logs)
	}
}

type offlineUpgradeFakeStore struct {
	*backupFakeStore
	updated map[string]store.AppInstance
}

func (s *offlineUpgradeFakeStore) GetAppInstance(id string) (store.AppInstance, error) {
	instance, _, err := clusterFixtureInstance(s.instances, id)
	return instance, err
}

func (s *offlineUpgradeFakeStore) SaveAppInstancesIfUnchanged(items []store.AppInstance) error {
	for _, item := range items {
		s.updated[item.ID] = item
	}
	return nil
}

type upgradeFakeRemote struct {
	views                    []upgradeView
	servers                  []store.Server
	rolled                   []int
	metadata, routerUpgraded bool
	firstRollBeforeTools     bool
	failServer               int
}

func (r *upgradeFakeRemote) UploadFile(_ context.Context, _ store.Server, _, _ string, _ os.FileMode) error {
	return nil
}

func (r *upgradeFakeRemote) Run(_ context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	var result adapter.CommandResult
	index := slices.IndexFunc(r.servers, func(registered store.Server) bool { return registered.ID == server.ID })
	switch {
	case strings.Contains(command, " 'probe' "):
		view := r.views[index]
		var output strings.Builder
		output.WriteString("marker\tlocal_uuid\tgroup_uuid\tlocal_version\texecuted_gtid\treadiness\n")
		fmt.Fprintf(&output, "__AIFAR_UPGRADE_LOCAL__\t%s\t%s\t%s\t%s\t1\n", view.uuid, view.group, view.version, view.gtid)
		output.WriteString("marker\tMEMBER_ID\tMEMBER_HOST\tMEMBER_PORT\tMEMBER_VERSION\tMEMBER_ROLE\tMEMBER_STATE\n")
		for _, member := range view.members {
			host, port, _ := strings.Cut(member.endpoint, ":")
			fmt.Fprintf(&output, "__AIFAR_UPGRADE_MEMBER__\t%s\t%s\t%s\t%s\t%s\t%s\n", member.uuid, host, port, member.version, member.role, member.state)
		}
		result.Stdout = output.String()
	case strings.Contains(command, " 'checker' "):
		result.Stdout = `{"targetVersion":"8.4.13","errorCount":0,"checksPerformed":[{}]}`
	case strings.Contains(command, " 'metadata-upgrade' "):
		r.metadata = true
	case strings.Contains(command, " 'tools' 'mysql-router' "):
		r.routerUpgraded = true
	case strings.Contains(command, " 'route-sql' "):
		result.Stdout = "__AIFAR_UPGRADE_ROUTER__\t" + r.views[index].uuid + "\t0\n"
	case strings.Contains(command, " 'server' "):
		if index == r.failServer {
			result.Stderr = "mysqli error with upgrade-test-password"
			return result, fmt.Errorf("systemd startup failed")
		}
		r.firstRollBeforeTools = r.firstRollBeforeTools || !r.metadata || !r.routerUpgraded
		r.rolled = append(r.rolled, index)
		r.views[index].version = "8.4.13"
		r.views[0].members[index].version = "8.4.13"
		if index == 1 {
			r.views[0].members[0].role = "PRIMARY"
			r.views[0].members[1].role = "SECONDARY"
		}
	}
	return result, nil
}

func newOfflineUpgradeFixture(t *testing.T) (*offlineUpgradeFakeStore, *upgradeFakeRemote, []store.Resource, registry.RunContext) {
	t.Helper()
	instances, servers := healthyClusterRequestFixtures()
	base := newBackupFakeStore(t)
	base.instances = instances
	base.servers = map[string]store.Server{}
	base.credential = store.Credential{Kind: "mysql", Username: "root", Status: "active", Secret: map[string]string{"password": "upgrade-test-password"}}
	clusterID := OfflineUpgradeClusterID(instances[0])
	base.cluster = store.AppCluster{ID: clusterID, App: "mysql", Name: "upgrade-test", Topology: "innodb-cluster"}
	views := upgradeTestViews()
	for i, instance := range instances {
		base.members = append(base.members, store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: instance.ServerID})
		base.servers[instance.ServerID] = servers[i]
		views[i].endpoint = servers[i].Host + ":3306"
		views[i].members[i].endpoint = views[i].endpoint
	}
	router := store.AppInstance{ID: "router-test", App: "mysql-router", ServerID: servers[0].ID, Version: "8.0.36", Topology: "router", Metadata: fmt.Sprintf(`{"clusterId":%q}`, clusterID)}
	base.instances = append(base.instances, router)
	archive := filepath.Join(t.TempDir(), "mysql-aifar-8.4.13-official-bundle.tar")
	archiveBytes := []byte("upgrade test bundle")
	if err := os.WriteFile(archive, archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	resources := []store.Resource{{App: "mysql", Part: "backend", Version: "8.4.13", Path: archive, SHA256: fmt.Sprintf("%x", sha256.Sum256(archiveBytes))}}
	return &offlineUpgradeFakeStore{backupFakeStore: base, updated: map[string]store.AppInstance{}}, &upgradeFakeRemote{views: views, servers: servers, failServer: -1}, resources,
		registry.RunContext{TaskID: "tsk_0123456789abcdef01234567", Language: "en", Log: &backupRecorder{}}
}
