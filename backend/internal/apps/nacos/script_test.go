package nacos

import (
	"strings"
	"testing"
)

func TestInstallNacosScriptRendersClusterConfig(t *testing.T) {
	script, err := installNacosScript(InstallScriptRequest{
		Version:       "2.4.3",
		Mode:          "cluster",
		WorkDir:       "/aifar/apps/_work/nacos",
		ArchivePath:   "/aifar/apps/_work/nacos/nacos-server-2.4.3.tar.gz",
		JDKPath:       "/aifar/apps/_work/nacos/jdk.tar.gz",
		InstallRoot:   "/aifar/apps/nacos",
		Port:          8848,
		GRPCPort:      9848,
		GRPCRaftPort:  9849,
		RaftPort:      7848,
		JVMXMS:        "512m",
		JVMXMX:        "512m",
		JVMXMN:        "256m",
		AdminUser:     "nacos",
		AdminPassword: "nacos",
		Database:      DatabaseOptions{Enabled: true, Host: "192.168.1.10", Port: 3306, Name: "nacos_config", User: "nacos", Password: "secret"},
		ClusterNodes:  []NacosClusterNode{{Host: "10.0.0.1", Port: 8848}, {Host: "10.0.0.2", Port: 8848}, {Host: "10.0.0.3", Port: 8848}},
	})
	if err != nil {
		t.Fatalf("installNacosScript returned error: %v", err)
	}
	for _, want := range []string{
		"10.0.0.1:8848",
		"nacos.core.auth.enabled=true",
		"spring.sql.init.platform=mysql",
		"allowPublicKeyRetrieval=true",
		"configure_nacos_admin_user",
		"nacos_try_configure_admin_user",
		"nacos_create_user",
		"nacos_update_user",
		"waiting for Nacos auth API to accept credential configuration",
		"warning: unable to authenticate to Nacos for credential configuration after retries",
		"Nacos readiness is OK, continuing",
		"Environment=\"CUSTOM_NACOS_MEMORY=-Xms$JVM_XMS -Xmx$JVM_XMX -Xmn$JVM_XMN\"",
		"ExecStart=$NACOS_HOME/bin/startup.sh -m $MODE",
		"Nacos cluster mode requires MySQL database configuration",
		"systemctl restart \"$SERVICE_NAME\"",
		"dump_nacos_diagnostics",
		"Nacos readiness endpoint is not healthy yet, but port $PORT is listening; continuing to credential retries",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("rendered script missing %q", want)
		}
	}
	if strings.Contains(script, `if [ "$NACOS_USER" = "nacos" ] && [ "$NACOS_PASSWORD" = "nacos" ]; then`) {
		t.Fatalf("default Nacos credentials must still be verified after install")
	}
	if strings.Contains(script, "Nacos credential verification failed") {
		t.Fatalf("credential verification must not fail an otherwise ready Nacos install")
	}
}

func TestInstallNacosScriptAllowsLocalStandaloneStorage(t *testing.T) {
	script, err := installNacosScript(InstallScriptRequest{
		Version:       "2.4.3",
		Mode:          "standalone",
		WorkDir:       "/aifar/apps/_work/nacos",
		ArchivePath:   "/aifar/apps/_work/nacos/nacos-server-2.4.3.tar.gz",
		JDKPath:       "/aifar/apps/_work/nacos/jdk.tar.gz",
		InstallRoot:   "/aifar/apps/nacos",
		Port:          8848,
		GRPCPort:      9848,
		GRPCRaftPort:  9849,
		RaftPort:      7848,
		JVMXMS:        "512m",
		JVMXMX:        "512m",
		JVMXMN:        "256m",
		AdminUser:     "ops",
		AdminPassword: "Nacos.123",
		Database:      DatabaseOptions{Enabled: false, Source: databaseSourceLocal},
		ClusterNodes:  []NacosClusterNode{{Host: "10.0.0.1", Port: 8848}, {Host: "10.0.0.2", Port: 8848}, {Host: "10.0.0.3", Port: 8848}},
	})
	if err != nil {
		t.Fatalf("installNacosScript returned error: %v", err)
	}
	for _, expected := range []string{
		"MODE='standalone'",
		"DB_ENABLED=0",
		"INIT_DATABASE=0",
		`if [ "$DB_ENABLED" = "1" ]; then`,
		`$SUDO rm -f "$NACOS_HOME/conf/cluster.conf"`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("standalone local storage script should contain %q", expected)
		}
	}
	if !strings.Contains(script, "NACOS_USER='ops'") || !strings.Contains(script, "NACOS_PASSWORD='Nacos.123'") {
		t.Fatalf("script should render custom Nacos credentials")
	}
}

func TestCheckNacosScriptPrefersReachabilityBeforeSystemd(t *testing.T) {
	script, err := checkNacosScript(CheckScriptRequest{
		InstallRoot: "/aifar/apps/nacos",
		Port:        8848,
	})
	if err != nil {
		t.Fatalf("checkNacosScript returned error: %v", err)
	}
	readinessIndex := strings.Index(script, "/nacos/v1/console/health/readiness")
	portIndex := strings.Index(script, "Nacos port is listening")
	systemdIndex := strings.Index(script, "systemctl is-active")
	if readinessIndex < 0 || portIndex < 0 || systemdIndex < 0 {
		t.Fatalf("check script missing readiness, port, or systemd checks:\n%s", script)
	}
	if readinessIndex > systemdIndex || portIndex > systemdIndex {
		t.Fatalf("check script should verify HTTP/port reachability before systemd status:\n%s", script)
	}
}
