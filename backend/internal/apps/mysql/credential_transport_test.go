package mysql

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

const credentialTransportSentinel = `S3cr'et"$\:@{}[]!`

func TestUploadMySQLCredentialContextFailsClosedWhenLocalRemovalFails(t *testing.T) {
	remote := &fakeRemote{}
	server := store.Server{ID: "srv-1", Host: "10.0.0.1"}
	credential := store.Credential{Kind: "mysql", Status: "active", Username: "root", Secret: map[string]string{"password": credentialTransportSentinel}}
	originalRemove := removeMySQLCredentialContextFile
	var retained string
	removeMySQLCredentialContextFile = func(name string) error {
		retained = name
		return errors.New("injected local removal failure " + credentialTransportSentinel)
	}
	defer func() {
		removeMySQLCredentialContextFile = originalRemove
		if retained != "" {
			_ = originalRemove(retained)
		}
	}()
	err := uploadMySQLCredentialContext(context.Background(), remote, server, credential, 3306, "/aifar/apps/mysql/_backup/task/secret-context.cnf")
	if err == nil || strings.Contains(err.Error(), credentialTransportSentinel) {
		t.Fatalf("local credential cleanup failure was not generic and fatal: %v", err)
	}
	if retained == "" {
		t.Fatal("local credential context cleanup was not attempted")
	}
}

func TestMySQLCredentialConsumersValidateRemoteContextBeforeEveryRead(t *testing.T) {
	commands := map[string]string{
		"backup inspection":    inspectBackupCommand("/aifar/apps/mysql/_backup/task", 3306),
		"cluster inspection":   inspectClusterMembersCommand("/aifar/apps/mysql/_backup/task", 3306),
		"router verification":  routerReadWriteVerificationCommand("/aifar/apps/mysql/_backup/task", 6446, "aifar_business"),
		"maintenance ping":     mysqlMaintenancePingCommand("/aifar/apps/mysql/_backup/task", store.Server{DeployDir: "/aifar/apps"}, store.AppInstance{Version: "8.0.36"}),
		"disaster MySQL Shell": mysqlShellJSCommand("/aifar/apps/mysql/_backup/task", 3306, "print('ok')"),
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			for _, boundary := range []string{"test -f", "test ! -L", "stat -c '%u'", "id -u", "stat -c '%a'", "= 600"} {
				if !strings.Contains(command, boundary) {
					t.Fatalf("credential read is missing %q validation: %s", boundary, command)
				}
			}
		})
	}
}

func TestInstallerTransportsCredentialOnlyIn0600Context(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := &installerFakeRemote{}
	err := NewInstaller(remote).Install(context.Background(), store.Server{ID: "srv-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"}, Bundle{
		Version: "8.0.36", ArchivePath: archive,
	}, InstallOptions{Port: 3306, RootUser: "root", RootPassword: credentialTransportSentinel}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(remote.installScript, credentialTransportSentinel) || strings.Contains(strings.Join(remote.commands, "\n"), credentialTransportSentinel) {
		t.Fatal("credential leaked into executable installer content or remote command")
	}
	found := false
	for _, upload := range remote.uploadDetails {
		if strings.Contains(upload.content, credentialTransportSentinel) {
			found = true
			if upload.mode != 0o600 {
				t.Fatalf("credential context mode = %04o, want 0600", upload.mode)
			}
			if !strings.Contains(upload.remotePath, "/_work/mysql-8.0.36-") {
				t.Fatalf("credential context path is not task-scoped: %s", upload.remotePath)
			}
		}
	}
	if !found {
		t.Fatal("standalone install did not upload a separate credential context")
	}
}

func TestClusterScriptsNeverRenderCredentialOrPasswordURI(t *testing.T) {
	for name, render := range map[string]func() (string, error){
		"bootstrap": func() (string, error) {
			return bootstrapInnoDBClusterScript(InnoDBClusterBootstrapScriptRequest{ClusterName: "aifarCluster", InstallRoot: "/aifar/apps/mysql", CredentialContextPath: "/aifar/apps/_work/mysql-credential-bootstrap/context/credential-context.json", Nodes: []InnoDBClusterNode{{Host: "10.0.0.1", Port: 3306}}})
		},
		"start": func() (string, error) {
			return startInnoDBClusterScript(InnoDBClusterStartScriptRequest{ClusterName: "aifarCluster", InstallRoot: "/aifar/apps/mysql", CredentialContextPath: "/aifar/apps/_work/mysql-credential-start/context/credential-context.json", Nodes: []InnoDBClusterNode{{Host: "10.0.0.1", Port: 3306}}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			script, err := render()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(script, credentialTransportSentinel) || strings.Contains(script, "rootPassword") {
				t.Fatalf("%s script contains credential or password-bearing URI", name)
			}
			if !strings.Contains(script, "os.loadTextFile") {
				t.Fatalf("%s script does not load a fixed credential context", name)
			}
		})
	}
}

func TestCheckUsesBoundCredentialAndNeverDefaultPassword(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "mysql", Version: "8.0.36", ServerID: "srv-1", Topology: "standalone", Metadata: `{"port":3306,"rootUser":"root"}`}
	s := &fakeStore{
		servers:     map[string]store.Server{"srv-1": {ID: "srv-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"}},
		instances:   []store.AppInstance{instance},
		credentials: map[string]store.Credential{"app-1": {Kind: "mysql", Status: "active", Username: "rotated", Secret: map[string]string{"password": credentialTransportSentinel}}},
	}
	remote := &fakeRemote{}
	_, err := NewService(s, remote).Check(context.Background(), CheckRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en", DefaultPassword: "forbidden-default"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	commands := remote.joinedCommands()
	if strings.Contains(commands, credentialTransportSentinel) || strings.Contains(commands, "forbidden-default") || strings.Contains(commands, "MYSQL_PWD=") {
		t.Fatalf("status credential leaked or default fallback used: %s", commands)
	}
	assertUploadedCredentialContext(t, remote.uploads, "srv-1", credentialTransportSentinel)
}

func TestClusterStartUsesDistinctCurrentMemberCredentials(t *testing.T) {
	now := time.Now()
	clusterID := "mysql_cluster_secure"
	instances := []store.AppInstance{
		mysqlClusterInstance("app-1", "srv-1", clusterID, "10.0.0.1:3306", now),
		mysqlClusterInstance("app-2", "srv-2", clusterID, "10.0.0.2:3306", now),
		mysqlClusterInstance("app-3", "srv-3", clusterID, "10.0.0.3:3306", now),
	}
	servers := map[string]store.Server{
		"srv-1": {ID: "srv-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
	}
	credentials := map[string]store.Credential{}
	for index, instance := range instances {
		credentials[instance.ID] = store.Credential{Kind: "mysql", Status: "active", Username: "admin" + string(rune('A'+index)), Secret: map[string]string{"password": credentialTransportSentinel + string(rune('1'+index))}}
	}
	s := &fakeStore{servers: servers, instances: instances, credentials: credentials}
	remote := &fakeRemote{primaryOutput: "10.0.0.1:3306\n"}
	err := NewService(s, remote).StartInnoDBCluster(context.Background(), StartClusterRequest{Instances: instances, Servers: []store.Server{servers["srv-1"], servers["srv-2"], servers["srv-3"]}, Language: "en", DefaultPassword: "forbidden-default"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	commands := remote.joinedCommands()
	if strings.Contains(commands, credentialTransportSentinel) || strings.Contains(commands, "forbidden-default") || strings.Contains(commands, "MYSQL_PWD=") {
		t.Fatalf("cluster start leaked credential or used fallback: %s", commands)
	}
	for index := range instances {
		assertUploadedCredentialContext(t, remote.uploads, "srv-1", credentialTransportSentinel+string(rune('1'+index)))
	}
}

func TestInstallerSanitizesRemoteFailureAndStillCleansCredentialArtifacts(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupAttempted := false
	remote := &installerFakeRemote{runHook: func(command string) (adapter.CommandResult, error) {
		if strings.Contains(command, "rm -f --") && strings.Contains(command, "mysql-credential.context") {
			cleanupAttempted = true
			return adapter.CommandResult{}, nil
		}
		if strings.Contains(command, "install-mysql.sh") {
			return adapter.CommandResult{Stdout: "output " + credentialTransportSentinel, Stderr: "error " + credentialTransportSentinel}, errors.New("remote " + credentialTransportSentinel)
		}
		return adapter.CommandResult{Stdout: "ok"}, nil
	}}
	log := &recordingLogger{}
	err := NewInstaller(remote).Install(context.Background(), store.Server{ID: "srv-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"}, Bundle{Version: "8.0.36", ArchivePath: archive}, InstallOptions{Port: 3306, RootUser: "root", RootPassword: credentialTransportSentinel}, log)
	if err == nil {
		t.Fatal("expected remote install failure")
	}
	if strings.Contains(err.Error(), credentialTransportSentinel) || strings.Contains(log.joined(), credentialTransportSentinel) {
		t.Fatal("remote install failure leaked the credential")
	}
	if !cleanupAttempted {
		t.Fatal("remote credential cleanup was not attempted after install failure")
	}
}

func TestInstallerCleanupFailureCannotPublishSuccess(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := &installerFakeRemote{runHook: func(command string) (adapter.CommandResult, error) {
		if strings.Contains(command, "rm -f --") && strings.Contains(command, "mysql-credential.context") {
			return adapter.CommandResult{}, errors.New("cleanup failed " + credentialTransportSentinel)
		}
		return adapter.CommandResult{Stdout: "ok"}, nil
	}}
	log := &recordingLogger{}
	err := NewInstaller(remote).Install(context.Background(), store.Server{ID: "srv-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"}, Bundle{Version: "8.0.36", ArchivePath: archive}, InstallOptions{Port: 3306, RootUser: "root", RootPassword: credentialTransportSentinel}, log)
	if err == nil {
		t.Fatal("cleanup failure must fail the state-changing install")
	}
	if strings.Contains(err.Error(), credentialTransportSentinel) {
		t.Fatal("cleanup error leaked credential material")
	}
	if strings.Contains(log.joined(), "installed and verified") {
		t.Fatalf("cleanup failure published installer success: %s", log.joined())
	}
}

func TestClusterCleanupFailureSuppressesRemoteCompletionOutput(t *testing.T) {
	remote := &installerFakeRemote{runHook: func(command string) (adapter.CommandResult, error) {
		if strings.Contains(command, "start-cluster.sh") {
			return adapter.CommandResult{Stdout: "MySQL InnoDB Cluster start completed"}, nil
		}
		if strings.Contains(command, "rm -rf --") && strings.Contains(command, "mysql-credential-start") {
			return adapter.CommandResult{}, errors.New("cleanup failed")
		}
		return adapter.CommandResult{}, nil
	}}
	log := &recordingLogger{}
	err := NewInstaller(remote).StartInnoDBCluster(context.Background(), store.Server{ID: "srv-1", DeployDir: "/aifar/apps"}, InnoDBClusterStartRequest{
		ClusterName: "aifarCluster", InstallRoot: "/aifar/apps/mysql",
		Connections: []mysqlConnectionCredential{{Host: "10.0.0.1", Port: 3306, User: "root", Password: credentialTransportSentinel}},
		Nodes:       []InnoDBClusterNode{{Host: "10.0.0.1", Port: 3306}},
	}, log)
	if err == nil {
		t.Fatal("cluster cleanup failure must fail")
	}
	if strings.Contains(log.joined(), "start completed") {
		t.Fatalf("cleanup failure published remote completion output: %s", log.joined())
	}
}

func TestStateChangingScriptsPublishCompletionOnlyAfterExplicitCleanup(t *testing.T) {
	tests := map[string]string{}
	install, err := installStandaloneScript(InstallScriptRequest{Version: "8.0.36", WorkDir: "/aifar/apps/_work/mysql-8.0.36-1", ArchivePath: "/tmp/mysql.tar", InstallRoot: "/aifar/apps/mysql", Port: 3306})
	if err != nil {
		t.Fatal(err)
	}
	tests["install"] = install
	bootstrap, err := bootstrapInnoDBClusterScript(InnoDBClusterBootstrapScriptRequest{ClusterName: "aifarCluster", InstallRoot: "/aifar/apps/mysql", CredentialContextPath: "/aifar/apps/_work/mysql-credential-bootstrap-1/credential-context.json", Nodes: []InnoDBClusterNode{{Host: "10.0.0.1", Port: 3306}}})
	if err != nil {
		t.Fatal(err)
	}
	tests["bootstrap"] = bootstrap
	start, err := startInnoDBClusterScript(InnoDBClusterStartScriptRequest{ClusterName: "aifarCluster", InstallRoot: "/aifar/apps/mysql", CredentialContextPath: "/aifar/apps/_work/mysql-credential-start-1/credential-context.json", Nodes: []InnoDBClusterNode{{Host: "10.0.0.1", Port: 3306}}})
	if err != nil {
		t.Fatal(err)
	}
	tests["start"] = start
	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			completion := strings.LastIndex(script, "completed")
			if name == "install" {
				completion = strings.LastIndex(script, "installed:")
			}
			cleanup := strings.LastIndex(script, "cleanup_secret_artifacts")
			if completion < 0 || strings.Count(script, "cleanup_secret_artifacts") < 3 || cleanup > completion {
				t.Fatalf("%s completion is not ordered after explicit cleanup", name)
			}
		})
	}
}

func TestInstallerAttemptsLocalCleanupWhenRemoteCleanupFails(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := &installerFakeRemote{runHook: func(command string) (adapter.CommandResult, error) {
		if strings.Contains(command, "rm -f --") && strings.Contains(command, "mysql-credential.context") {
			return adapter.CommandResult{}, errors.New("remote cleanup " + credentialTransportSentinel)
		}
		return adapter.CommandResult{Stdout: "ok"}, nil
	}}
	originalRemove := removeMySQLCredentialContextFile
	localAttempts := 0
	removeMySQLCredentialContextFile = func(name string) error {
		localAttempts++
		_ = os.Remove(name)
		return errors.New("local cleanup " + credentialTransportSentinel)
	}
	t.Cleanup(func() { removeMySQLCredentialContextFile = originalRemove })
	err := NewInstaller(remote).Install(context.Background(), store.Server{ID: "srv-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"}, Bundle{Version: "8.0.36", ArchivePath: archive}, InstallOptions{Port: 3306, RootUser: "root", RootPassword: credentialTransportSentinel}, installerTestLogger{})
	if err == nil || localAttempts != 1 {
		t.Fatalf("remote failure must not skip local cleanup: err=%v attempts=%d", err, localAttempts)
	}
	if strings.Contains(err.Error(), credentialTransportSentinel) {
		t.Fatal("combined cleanup failure leaked credential material")
	}
}

func TestStatusCredentialCleanupAttemptsRemoteAndLocalAndReturnsGenericError(t *testing.T) {
	remoteCleanupAttempts := 0
	remote := &installerFakeRemote{runHook: func(command string) (adapter.CommandResult, error) {
		if strings.Contains(command, "rm -rf --") && strings.Contains(command, "_backup/credential_") {
			remoteCleanupAttempts++
			return adapter.CommandResult{}, errors.New("remote private path " + credentialTransportSentinel)
		}
		return adapter.CommandResult{Stdout: "runtimeStatus=running\n"}, nil
	}}
	localCleanupAttempts := 0
	originalRemove := removeMySQLCredentialContextFile
	removeMySQLCredentialContextFile = func(name string) error {
		localCleanupAttempts++
		_ = os.Remove(name)
		return errors.New("local private path " + credentialTransportSentinel)
	}
	t.Cleanup(func() { removeMySQLCredentialContextFile = originalRemove })
	instance := store.AppInstance{ID: "app-1", App: "mysql", Version: "8.0.36", Metadata: `{"port":3306}`}
	credential := store.Credential{Kind: "mysql", Status: "active", Username: "root", Secret: map[string]string{"password": credentialTransportSentinel}}
	_, err := NewService(&fakeStore{}, remote).runMySQLCredentialCommand(context.Background(), store.Server{ID: "srv-1"}, instance, credential, fakeLogger{}, func(string) string { return "printf ok" })
	if err == nil || remoteCleanupAttempts != 1 || localCleanupAttempts != 1 {
		t.Fatalf("credential cleanup attempts: err=%v remote=%d local=%d", err, remoteCleanupAttempts, localCleanupAttempts)
	}
	if strings.Contains(err.Error(), credentialTransportSentinel) || strings.Contains(err.Error(), "private path") {
		t.Fatalf("credential cleanup error exposed private detail: %v", err)
	}
}

func TestClusterStartMissingBoundCredentialFailsBeforeAdminAPIMutation(t *testing.T) {
	now := time.Now()
	clusterID := "mysql_cluster_missing_credential"
	instances := []store.AppInstance{
		mysqlClusterInstance("app-1", "srv-1", clusterID, "10.0.0.1:3306", now),
		mysqlClusterInstance("app-2", "srv-2", clusterID, "10.0.0.2:3306", now),
		mysqlClusterInstance("app-3", "srv-3", clusterID, "10.0.0.3:3306", now),
	}
	servers := map[string]store.Server{
		"srv-1": {ID: "srv-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
	}
	credentials := testMySQLCredentials(instances)
	delete(credentials, "app-2")
	s := &fakeStore{servers: servers, instances: instances, credentials: credentials}
	remote := &fakeRemote{}
	err := NewService(s, remote).StartInnoDBCluster(context.Background(), StartClusterRequest{Instances: instances, Servers: []store.Server{servers["srv-1"], servers["srv-2"], servers["srv-3"]}, Language: "en", DefaultPassword: "forbidden-default"}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("missing member credential must fail closed")
	}
	if commands := remote.joinedCommands(); strings.Contains(commands, "rebootClusterFromCompleteOutage") || strings.Contains(commands, "rejoinInstance") {
		t.Fatalf("AdminAPI mutation ran before all member credentials were resolved: %s", commands)
	}
}

func assertUploadedCredentialContext(t *testing.T, uploads []credentialTransportUpload, serverID, secret string) {
	t.Helper()
	escaped := strings.ReplaceAll(secret, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	for _, upload := range uploads {
		if upload.serverID == serverID && upload.mode == 0o600 && (strings.Contains(upload.content, secret) || strings.Contains(upload.content, escaped)) {
			return
		}
	}
	t.Fatalf("0600 credential context for %s was not uploaded to %s", secret, serverID)
}
