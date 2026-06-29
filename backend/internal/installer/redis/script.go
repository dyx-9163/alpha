package redis

import (
	"bytes"
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

var redisStandaloneInstallTemplate = template.Must(template.New("redis-standalone-install").
	Funcs(redisScriptFuncs).
	Parse(standaloneInstallScriptTemplate))

var redisStandaloneUninstallTemplate = template.Must(template.New("redis-standalone-uninstall").
	Funcs(redisScriptFuncs).
	Parse(standaloneUninstallScriptTemplate))

var redisSentinelConfigureTemplate = template.Must(template.New("redis-sentinel-configure").
	Funcs(redisScriptFuncs).
	Parse(sentinelConfigureScriptTemplate))

var redisSentinelUninstallTemplate = template.Must(template.New("redis-sentinel-uninstall").
	Funcs(redisScriptFuncs).
	Parse(sentinelUninstallScriptTemplate))

var redisClusterEnableNodeTemplate = template.Must(template.New("redis-cluster-enable-node").
	Funcs(redisScriptFuncs).
	Parse(clusterEnableNodeScriptTemplate))

var redisClusterBootstrapTemplate = template.Must(template.New("redis-cluster-bootstrap").
	Funcs(redisScriptFuncs).
	Parse(clusterBootstrapScriptTemplate))

type standaloneInstallScriptData struct {
	Version     string
	WorkDir     string
	ArchivePath string
	InstallRoot string
	Port        int
	Password    string
}

type standaloneUninstallScriptData struct {
	Version     string
	InstallRoot string
	Port        int
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
	return renderRedisScript(redisStandaloneInstallTemplate, standaloneInstallScriptData{
		Version:     version,
		WorkDir:     workDir,
		ArchivePath: archivePath,
		InstallRoot: installRoot,
		Port:        port,
		Password:    password,
	})
}

func uninstallStandaloneScript(version, installRoot string, port int) (string, error) {
	return renderRedisScript(redisStandaloneUninstallTemplate, standaloneUninstallScriptData{
		Version:     version,
		InstallRoot: installRoot,
		Port:        port,
	})
}

func configureSentinelNodeScript(req SentinelNodeConfig) (string, error) {
	return renderRedisScript(redisSentinelConfigureTemplate, req)
}

func uninstallSentinelNodeScript(version, installRoot string, redisPort, sentinelPort int) (string, error) {
	return renderRedisScript(redisSentinelUninstallTemplate, struct {
		Version      string
		InstallRoot  string
		RedisPort    int
		SentinelPort int
	}{
		Version:      version,
		InstallRoot:  installRoot,
		RedisPort:    redisPort,
		SentinelPort: sentinelPort,
	})
}

func enableClusterNodeScript(req ClusterNodeConfig) (string, error) {
	return renderRedisScript(redisClusterEnableNodeTemplate, req)
}

func bootstrapClusterScript(req ClusterBootstrapConfig) (string, error) {
	return renderRedisScript(redisClusterBootstrapTemplate, req)
}

func renderRedisScript(tpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
