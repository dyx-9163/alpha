package mysql

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
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

func TestOfflineUpgradeWaitForViewsLogsConnectionRefusedAsReadiness(t *testing.T) {
	const refused = "MySQL Error 2003 (HY000): Can't connect to MySQL server on '127.0.0.1:3306' (111)"
	const accessDenied = "MySQL Error 1045 (28000): Access denied for user"
	for _, tc := range []struct {
		name, stderr    string
		recovers, waits bool
	}{
		{"restart recovers", refused, true, true},
		{"wait ends before recovery", refused, false, true},
		{"authentication fails", accessDenied, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, remote, _, run := newOfflineUpgradeFixture(t)
			log := &upgradeProbeLog{}
			run.Log = log
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &upgradeProbeFake{upgradeFakeRemote: remote, failOnce: func() (adapter.CommandResult, error) {
				if !tc.recovers {
					cancel()
				}
				return adapter.CommandResult{Stderr: "\x1b[31mERROR: \x1b[0m" + tc.stderr}, errors.New("remote command exited")
			}}
			upgrade := offlineUpgrade{
				service:  NewService(nil, probe),
				run:      run,
				versions: map[int]string{0: "8.0.36", 1: "8.0.36", 2: "8.0.36"},
			}
			for i, server := range remote.servers[:3] {
				upgrade.nodes = append(upgrade.nodes, upgradeNode{server: server, port: 3306, endpoint: remote.views[i].endpoint})
			}
			if _, _, err := upgrade.waitForViews(ctx); tc.recovers {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, context.Canceled) || (tc.waits && !strings.Contains(err.Error(), "not accepting connections")) {
				t.Fatalf("terminal error lost the probe diagnosis: %v", err)
			}
			if got := strings.Contains(strings.Join(log.messages, "\n"), "not accepting connections"); got != tc.waits {
				t.Errorf("readiness message logged = %t, want %t", got, tc.waits)
			}
			if got := strings.Contains(strings.Join(log.errors, "\n"), tc.stderr); got == tc.waits {
				t.Errorf("raw probe error logged as error = %t, want %t", got, !tc.waits)
			}
		})
	}
}

type upgradeProbeLog struct {
	backupRecorder
	errors []string
}

func (l *upgradeProbeLog) Error(format string, args ...any) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

type upgradeProbeFake struct {
	*upgradeFakeRemote
	failOnce func() (adapter.CommandResult, error)
}

func (r *upgradeProbeFake) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if r.failOnce != nil && strings.Contains(command, " 'probe' ") {
		fail := r.failOnce
		r.failOnce = nil
		return fail()
	}
	return r.upgradeFakeRemote.Run(ctx, server, command)
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

func TestOfflineUpgradeExtractsServiceTreesWithRuntimeOwner(t *testing.T) {
	requireLinuxDescriptors(t)
	script, _, found := strings.Cut(offlineUpgradeScript, "\ncase \"$phase\" in\n")
	if !found {
		t.Fatal("could not load upgrade shell functions")
	}
	for _, tc := range []struct {
		binary, owner string
	}{
		{"mysqld", "aifar-mysql:aifar-mysql"},
		{"mysqlrouter", "aifar-router:aifar-router"},
		{"mysqlsh", ""},
	} {
		t.Run(tc.binary, func(t *testing.T) {
			work := t.TempDir()
			source := filepath.Join(work, "source")
			mustMkdirLogical(t, filepath.Join(source, "bin"), 0o700)
			writeLogicalTestFile(t, filepath.Join(source, "bin", tc.binary), "", 0o700)
			mustMkdirLogical(t, filepath.Join(work, "bundles"), 0o700)
			archive := exec.Command("tar", "-cf", filepath.Join(work, "bundles", "test.tar"), "-C", work, "source")
			if output, err := archive.CombinedOutput(); err != nil {
				t.Fatalf("create fixture archive: %v\n%s", err, output)
			}
			scriptPath := filepath.Join(work, "test.sh")
			writeLogicalTestFile(t, scriptPath, script+`
as_root() {
  if [[ "$1" == chown ]]; then
    printf '%s\n' "$@" > "$work/ownership-args"
  else
    "$@"
  fi
}
extract_tree test.tar "$work/expanded" source "bin/${args[0]}" "$work/target"
`, 0o700)
			extract := exec.Command("bash", scriptPath, "stage", "mysql", work, work, work, "test", "3306", tc.binary)
			if output, err := extract.CombinedOutput(); err != nil {
				t.Fatalf("extract upgrade tree: %v\n%s", err, output)
			}
			args, err := os.ReadFile(filepath.Join(work, "ownership-args"))
			if tc.owner == "" {
				if !os.IsNotExist(err) {
					t.Fatal("admin-only MySQL Shell tree ownership should be preserved")
				}
				return
			}
			want := strings.Join([]string{"chown", "-hR", "--", tc.owner, filepath.Join(work, "target"), ""}, "\n")
			if err != nil || string(args) != want {
				t.Errorf("candidate ownership command = %q, error = %v, want %q", args, err, want)
			}
		})
	}
}

func TestOfflineUpgradeCheckerOnReadOnlySecondary(t *testing.T) {
	requireLinuxDescriptors(t)
	for _, tc := range []struct {
		name, expectedRole, localStatus, errorCount string
		wantPass, wantExclude                       bool
	}{
		{"read-only secondary", "SECONDARY", "SECONDARY:1", "0", true, true},
		{"writable secondary", "SECONDARY", "SECONDARY:0", "0", true, false},
		{"primary", "PRIMARY", "PRIMARY:0", "0", true, false},
		{"role changed", "SECONDARY", "PRIMARY:0", "0", false, false},
		{"member not online", "SECONDARY", "", "0", false, false},
		{"secondary has another error", "SECONDARY", "SECONDARY:1", "1", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			work := filepath.Join(root, "work")
			mustMkdirLogical(t, work, 0o700)
			writeLogicalTestFile(t, filepath.Join(work, "upgrade.sh"), offlineUpgradeScript, 0o700)
			writeLogicalTestFile(t, filepath.Join(work, "credential.cnf"), "", 0o600)
			clientDir := filepath.Join(root, "mysql-shell", "bin")
			mustMkdirLogical(t, clientDir, 0o700)
			writeLogicalTestFile(t, filepath.Join(clientDir, "mysqlsh"), `#!/usr/bin/env bash
for query in "$@"; do
  if [[ "$query" == *'SELECT'*'replication_group_members'* && "$query" != *'SET GLOBAL'* ]]; then
    printf 'checker_member\n%s\n' "$TEST_LOCAL_STATUS"
    exit 0
  fi
done
exit 1
`, 0o700)
			checkerDir := filepath.Join(root, "mysql-shell.next-test", "bin")
			mustMkdirLogical(t, checkerDir, 0o700)
			writeLogicalTestFile(t, filepath.Join(checkerDir, "mysqlsh"), `#!/usr/bin/env bash
printf '%s\n' "$@" > "$TEST_CHECKER_ARGS"
if [[ "$TEST_LOCAL_STATUS" == 'SECONDARY:1' && " $* " != *' --exclude=checkTableCommand '* ]]; then
  exit 1
fi
printf '{"targetVersion":"8.4.13","errorCount":%s,"checksPerformed":[{}]}\n' "$TEST_ERROR_COUNT"
`, 0o700)
			argsPath := filepath.Join(work, "checker-args")
			cmd := exec.Command("bash", filepath.Join(work, "upgrade.sh"), "checker", "mysql", root, work, root, "test", "3306", tc.expectedRole)
			cmd.Env = append(os.Environ(), "TEST_LOCAL_STATUS="+tc.localStatus, "TEST_CHECKER_ARGS="+argsPath, "TEST_ERROR_COUNT="+tc.errorCount)
			output, err := cmd.Output()
			reportPassed, _ := upgradeCheckerPassed(string(output))
			if passed := err == nil && reportPassed; passed != tc.wantPass {
				t.Errorf("precheck passed = %t, want %t, script error = %v", passed, tc.wantPass, err)
			}
			args, err := os.ReadFile(argsPath)
			if tc.localStatus == "" || !strings.HasPrefix(tc.localStatus, tc.expectedRole+":") {
				if !os.IsNotExist(err) {
					t.Fatal("checker ran without the expected online member role")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if excluded := strings.Contains(string(args), "--exclude=checkTableCommand\n"); excluded != tc.wantExclude {
				t.Errorf("table check excluded = %t, want %t", excluded, tc.wantExclude)
			}
		})
	}
}

const writeSetCheckerReport = `{
  "targetVersion":"8.4.13",
  "errorCount":1,
  "checksPerformed":[
    {"id":"sysVars","status":"OK","detectedProblems":[
      {"level":"Error","dbObject":"binlog_transaction_dependency_tracking","dbObjectType":"SystemVariable",
       "description":"variable removed at version 8.4.0 is set to WRITESET (EXPLICIT).."},
      {"level":"Warning","dbObject":"another_variable","dbObjectType":"SystemVariable"}
    ]},
    {"id":"columnDefinition","status":"OK"}
  ]
}`

func TestOfflineUpgradeCheckerAcceptsOnlyRemediatedWriteSet(t *testing.T) {
	for _, tc := range []struct {
		name, from, to string
		want           bool
	}{
		{"known explicit WRITESET", "", "", true},
		{"another error", `"Warning"`, `"Error"`, false},
		{"other variable", "binlog_transaction_dependency_tracking", "another_variable", false},
		{"other check", "sysVars", "otherCheck", false},
		{"persisted value", "(EXPLICIT)", "(PERSISTED)", false},
		{"other replication behavior", "WRITESET (", "WRITESET_SESSION (", false},
		{"unexpected report target", "8.4.13", "8.4.12", false},
		{"unaccounted error", `"errorCount":1`, `"errorCount":2`, false},
		{"missing error count", `"errorCount":1,`, "", false},
		{"check could not run", `"columnDefinition","status":"OK"`, `"columnDefinition","status":"ERROR"`, false},
		{"check configuration error", `"columnDefinition","status":"OK"`, `"columnDefinition","status":"CONFIGURATION_ERROR"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := writeSetCheckerReport
			if tc.from != "" {
				report = strings.Replace(report, tc.from, tc.to, 1)
			}
			passed, handled := upgradeCheckerPassed(report)
			if passed != tc.want || handled != tc.want {
				t.Errorf("checker passed = %t, WRITESET handled = %t, want both %t", passed, handled, tc.want)
			}
		})
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
	remote.checkerReport = writeSetCheckerReport
	service := NewService(data, remote)
	if err := service.RunOfflineUpgrade(context.Background(), data.instances[0], resources, run); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(remote.rolled, []int{0, 2, 1}) || remote.firstRollBeforeTools {
		t.Fatalf("unexpected rollout order or premature server switch: %v", remote.rolled)
	}
	if !slices.Equal(remote.checkerRoles, []string{"SECONDARY", "PRIMARY", "SECONDARY"}) {
		t.Fatalf("upgrade checker did not use validated member roles: %v", remote.checkerRoles)
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

func TestOfflineUpgradeRecordsVersionsAcrossRuntimeChecks(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql", Version: OfflineUpgradeSourceVersion, ServerID: "server-1",
		Topology: "innodb-cluster", Status: "running",
		Metadata: `{"clusterId":"cluster-test","endpoint":"10.0.0.1:3306","role":"secondary"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db, nil)
	u := offlineUpgrade{
		data: db, bundle: Bundle{ArchivePath: "target.tar"},
		run:      registry.RunContext{TaskID: "tsk_0123456789abcdef01234567"},
		verified: map[string]store.AppInstanceVersionUpdate{},
	}
	u.markVerified(upgradeNode{instance: node, backup: "/backups/mysql"}, false)
	if err := service.markInnoDBClusterPrimary(node, "10.0.0.1:3306", map[string]any{"observed": "latest"}); err != nil {
		t.Fatal(err)
	}
	if err := u.saveVerified(); err != nil {
		t.Fatal(err)
	}
	if err := service.markInstanceStatus(node, "failed", nil); err != nil {
		t.Fatal(err)
	}
	actual, err := db.GetAppInstance(node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Version != OfflineUpgradeTargetVersion || actual.Status != "running" ||
		!strings.Contains(actual.Metadata, `"role":"primary"`) || !strings.Contains(actual.Metadata, `"observed":"latest"`) ||
		!strings.Contains(actual.Metadata, `"mysqlShellVersion":"26.10.0"`) {
		t.Fatalf("registration or latest runtime observation was lost: %+v", actual)
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

func (s *offlineUpgradeFakeStore) SaveAppInstanceVersionsIfUnchanged(updates []store.AppInstanceVersionUpdate) error {
	for _, update := range updates {
		item := update.Expected
		metadata := appMetadata(item)
		maps.Copy(metadata, update.Metadata)
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		item.Version, item.Metadata = update.Version, string(encoded)
		s.updated[item.ID] = item
	}
	return nil
}

type upgradeFakeRemote struct {
	views                    []upgradeView
	servers                  []store.Server
	rolled                   []int
	checkerRoles             []string
	checkerReport            string
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
		lastArg := command[strings.LastIndexByte(command, ' ')+1:]
		r.checkerRoles = append(r.checkerRoles, strings.Trim(lastArg, "'"))
		result.Stdout = `{"targetVersion":"8.4.13","errorCount":0,"checksPerformed":[{}]}`
		if r.checkerReport != "" {
			result.Stdout = r.checkerReport
		}
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
