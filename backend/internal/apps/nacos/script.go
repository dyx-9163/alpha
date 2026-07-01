package nacos

import (
	_ "embed"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
)

//go:embed templates/install.sh
var installScriptTemplate string

//go:embed templates/uninstall.sh
var uninstallScriptTemplate string

//go:embed templates/check.sh
var checkScriptTemplate string

var nacosScriptFuncs = template.FuncMap{
	"shq": installerkit.ShellQuote,
}

type NacosClusterNode struct {
	ID   string
	Name string
	Host string
	Port int
}

type InstallScriptRequest struct {
	Version      string
	Mode         string
	WorkDir      string
	ArchivePath  string
	JDKPath      string
	InstallRoot  string
	Port         int
	GRPCPort     int
	GRPCRaftPort int
	RaftPort     int
	JVMXMS       string
	JVMXMX       string
	JVMXMN       string
	Database     DatabaseOptions
	ClusterNodes []NacosClusterNode
}

type UninstallScriptRequest struct {
	Version           string
	InstallRoot       string
	LegacyInstallRoot string
	Port              int
}

type CheckScriptRequest struct {
	InstallRoot string
	Port        int
}

func installNacosScript(req InstallScriptRequest) (string, error) {
	return installerkit.RenderTemplate("nacos", "install.sh", "nacos-install", installScriptTemplate, nacosScriptFuncs, req)
}

func uninstallNacosScript(req UninstallScriptRequest) (string, error) {
	return installerkit.RenderTemplate("nacos", "uninstall.sh", "nacos-uninstall", uninstallScriptTemplate, nacosScriptFuncs, req)
}

func checkNacosScript(req CheckScriptRequest) (string, error) {
	return installerkit.RenderTemplate("nacos", "check.sh", "nacos-check", checkScriptTemplate, nacosScriptFuncs, req)
}
