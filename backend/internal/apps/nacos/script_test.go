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
		"nacos_create_user",
		"nacos_update_user",
		"Environment=\"CUSTOM_NACOS_MEMORY=-Xms$JVM_XMS -Xmx$JVM_XMX -Xmn$JVM_XMN\"",
		"ExecStart=$NACOS_HOME/bin/startup.sh -m $MODE",
		"systemctl restart \"$SERVICE_NAME\"",
		"dump_nacos_diagnostics",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("rendered script missing %q", want)
		}
	}
	if strings.Contains(script, `if [ "$NACOS_USER" = "nacos" ] && [ "$NACOS_PASSWORD" = "nacos" ]; then`) {
		t.Fatalf("default Nacos credentials must still be verified after install")
	}
}

func TestInstallNacosScriptAllowsLocalClusterStorage(t *testing.T) {
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
		AdminUser:     "ops",
		AdminPassword: "Nacos.123",
		Database:      DatabaseOptions{Enabled: false, Source: databaseSourceLocal},
		ClusterNodes:  []NacosClusterNode{{Host: "10.0.0.1", Port: 8848}, {Host: "10.0.0.2", Port: 8848}, {Host: "10.0.0.3", Port: 8848}},
	})
	if err != nil {
		t.Fatalf("installNacosScript returned error: %v", err)
	}
	for _, expected := range []string{
		"DB_ENABLED=0",
		"INIT_DATABASE=0",
		`if [ "$DB_ENABLED" = "1" ]; then`,
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("local storage script should contain %q", expected)
		}
	}
	if !strings.Contains(script, "NACOS_USER='ops'") || !strings.Contains(script, "NACOS_PASSWORD='Nacos.123'") {
		t.Fatalf("script should render custom Nacos credentials")
	}
}
