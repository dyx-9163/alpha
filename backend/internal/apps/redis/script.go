package redis

import (
	_ "embed"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
)

//go:embed templates/standalone/install.sh
var standaloneInstallScriptTemplate string

//go:embed templates/standalone/uninstall.sh
var standaloneUninstallScriptTemplate string

//go:embed templates/sentinel/configure-node.sh
var sentinelConfigureScriptTemplate string

//go:embed templates/sentinel/uninstall-node.sh
var sentinelUninstallScriptTemplate string

//go:embed templates/cluster/enable-node.sh
var clusterEnableNodeScriptTemplate string

//go:embed templates/cluster/bootstrap.sh
var clusterBootstrapScriptTemplate string

var redisScriptFuncs = template.FuncMap{
	"shq": installerkit.ShellQuote,
}

type standaloneInstallScriptData struct {
	Version      string
	WorkDir      string
	ArchivePath  string
	InstallRoot  string
	Port         int
	Password     string
	StartService bool
}

type standaloneUninstallScriptData struct {
	Version           string
	InstallRoot       string
	LegacyInstallRoot string
	Port              int
}

type SentinelNodeConfig struct {
	Version      string
	InstallRoot  string
	RedisPort    int
	SentinelPort int
	Password     string
	MasterName   string
	MasterHost   string
	MasterPort   int
	Quorum       int
	Role         string
}

type ClusterNodeConfig struct {
	Version     string
	InstallRoot string
	Port        int
	Password    string
}

type ClusterBootstrapConfig struct {
	Version     string
	InstallRoot string
	Port        int
	Password    string
	Replicas    int
	Nodes       []ClusterBootstrapNode
}

type ClusterBootstrapNode struct {
	Host string
	Port int
}

func installStandaloneScript(version, workDir, archivePath, installRoot string, port int, password string) (string, error) {
	return installRedisScript(version, workDir, archivePath, installRoot, port, password, true)
}

func installRedisBinariesScript(version, workDir, archivePath, installRoot string, port int, password string) (string, error) {
	return installRedisScript(version, workDir, archivePath, installRoot, port, password, false)
}

func installRedisScript(version, workDir, archivePath, installRoot string, port int, password string, startService bool) (string, error) {
	return installerkit.RenderTemplate("redis", "standalone/install.sh", "redis-standalone-install", standaloneInstallScriptTemplate, redisScriptFuncs, standaloneInstallScriptData{
		Version:      version,
		WorkDir:      workDir,
		ArchivePath:  archivePath,
		InstallRoot:  installRoot,
		Port:         port,
		Password:     password,
		StartService: startService,
	})
}

func uninstallStandaloneScript(version, installRoot, legacyInstallRoot string, port int) (string, error) {
	return installerkit.RenderTemplate("redis", "standalone/uninstall.sh", "redis-standalone-uninstall", standaloneUninstallScriptTemplate, redisScriptFuncs, standaloneUninstallScriptData{
		Version:           version,
		InstallRoot:       installRoot,
		LegacyInstallRoot: legacyInstallRoot,
		Port:              port,
	})
}

func configureSentinelNodeScript(req SentinelNodeConfig) (string, error) {
	return installerkit.RenderTemplate("redis", "sentinel/configure-node.sh", "redis-sentinel-configure", sentinelConfigureScriptTemplate, redisScriptFuncs, req)
}

func uninstallSentinelNodeScript(version, installRoot, legacyInstallRoot string, redisPort, sentinelPort int) (string, error) {
	return installerkit.RenderTemplate("redis", "sentinel/uninstall-node.sh", "redis-sentinel-uninstall", sentinelUninstallScriptTemplate, redisScriptFuncs, struct {
		Version           string
		InstallRoot       string
		LegacyInstallRoot string
		RedisPort         int
		SentinelPort      int
	}{
		Version:           version,
		InstallRoot:       installRoot,
		LegacyInstallRoot: legacyInstallRoot,
		RedisPort:         redisPort,
		SentinelPort:      sentinelPort,
	})
}

func enableClusterNodeScript(req ClusterNodeConfig) (string, error) {
	return installerkit.RenderTemplate("redis", "cluster/enable-node.sh", "redis-cluster-enable-node", clusterEnableNodeScriptTemplate, redisScriptFuncs, req)
}

func bootstrapClusterScript(req ClusterBootstrapConfig) (string, error) {
	return installerkit.RenderTemplate("redis", "cluster/bootstrap.sh", "redis-cluster-bootstrap", clusterBootstrapScriptTemplate, redisScriptFuncs, req)
}
