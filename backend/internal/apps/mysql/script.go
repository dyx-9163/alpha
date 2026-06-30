package mysql

import (
	_ "embed"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
)

//go:embed templates/standalone/install.sh
var standaloneInstallScriptTemplate string

//go:embed templates/standalone/uninstall.sh
var standaloneUninstallScriptTemplate string

//go:embed templates/innodb-cluster/bootstrap.sh
var innodbClusterBootstrapScriptTemplate string

var mysqlScriptFuncs = template.FuncMap{
	"shq": installerkit.ShellQuote,
}

func installStandaloneScript(req InstallScriptRequest) (string, error) {
	return installerkit.RenderTemplate("mysql", "standalone/install.sh", "mysql-standalone-install", standaloneInstallScriptTemplate, mysqlScriptFuncs, req)
}

func uninstallStandaloneScript(version, installRoot, legacyInstallRoot string, port int) (string, error) {
	return installerkit.RenderTemplate("mysql", "standalone/uninstall.sh", "mysql-standalone-uninstall", standaloneUninstallScriptTemplate, mysqlScriptFuncs, UninstallScriptRequest{
		Version:           version,
		InstallRoot:       installRoot,
		LegacyInstallRoot: legacyInstallRoot,
		Port:              port,
	})
}

func bootstrapInnoDBClusterScript(req InnoDBClusterBootstrapRequest) (string, error) {
	return installerkit.RenderTemplate("mysql", "innodb-cluster/bootstrap.sh", "mysql-innodb-cluster-bootstrap", innodbClusterBootstrapScriptTemplate, mysqlScriptFuncs, req)
}

type InstallScriptRequest struct {
	Version      string
	WorkDir      string
	ArchivePath  string
	InstallRoot  string
	ReportHost   string
	Port         int
	ServerID     uint32
	RootUser     string
	RootPassword string
}

type UninstallScriptRequest struct {
	Version           string
	InstallRoot       string
	LegacyInstallRoot string
	Port              int
}

type InnoDBClusterBootstrapRequest struct {
	ClusterName  string
	InstallRoot  string
	RootUser     string
	RootPassword string
	Nodes        []InnoDBClusterNode
}

type InnoDBClusterNode struct {
	Host string
	Port int
}
