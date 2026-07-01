package nacos

import (
	"strings"
	"testing"
)

func TestInstallNacosScriptRendersClusterConfig(t *testing.T) {
	script, err := installNacosScript(InstallScriptRequest{
		Version:      "2.4.3",
		Mode:         "cluster",
		WorkDir:      "/aifar/apps/_work/nacos",
		ArchivePath:  "/aifar/apps/_work/nacos/nacos-server-2.4.3.tar.gz",
		JDKPath:      "/aifar/apps/_work/nacos/jdk.tar.gz",
		InstallRoot:  "/aifar/apps/nacos",
		Port:         8848,
		GRPCPort:     9848,
		GRPCRaftPort: 9849,
		RaftPort:     7848,
		JVMXMS:       "512m",
		JVMXMX:       "512m",
		JVMXMN:       "256m",
		Database:     DatabaseOptions{Enabled: true, Host: "192.168.1.10", Port: 3306, Name: "nacos_config", User: "nacos", Password: "secret"},
		ClusterNodes: []NacosClusterNode{{Host: "10.0.0.1", Port: 8848}, {Host: "10.0.0.2", Port: 8848}, {Host: "10.0.0.3", Port: 8848}},
	})
	if err != nil {
		t.Fatalf("installNacosScript returned error: %v", err)
	}
	for _, want := range []string{
		"10.0.0.1:8848",
		"spring.sql.init.platform=mysql",
		"allowPublicKeyRetrieval=true",
		"Environment=\"CUSTOM_NACOS_MEMORY=-Xms$JVM_XMS -Xmx$JVM_XMX -Xmn$JVM_XMN\"",
		"ExecStart=$NACOS_HOME/bin/startup.sh -m $MODE",
		"systemctl restart \"$SERVICE_NAME\"",
		"dump_nacos_diagnostics",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("rendered script missing %q", want)
		}
	}
}
