package mysql

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
)

//go:embed templates/standalone/install.sh
var standaloneInstallScriptTemplate string

//go:embed templates/standalone/uninstall.sh
var standaloneUninstallScriptTemplate string

//go:embed templates/innodb-cluster/bootstrap.sh
var innodbClusterBootstrapScriptTemplate string

//go:embed templates/innodb-cluster/start.sh
var innodbClusterStartScriptTemplate string

//go:embed templates/backup/disaster-rebuild.sh
var disasterRebuildScriptTemplate string

var mysqlScriptFuncs = selinux.AddTemplateFuncs(template.FuncMap{
	"shq": installerkit.ShellQuote,
})

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

func bootstrapInnoDBClusterScript(req InnoDBClusterBootstrapScriptRequest) (string, error) {
	return installerkit.RenderTemplate("mysql", "innodb-cluster/bootstrap.sh", "mysql-innodb-cluster-bootstrap", innodbClusterBootstrapScriptTemplate, mysqlScriptFuncs, req)
}

func startInnoDBClusterScript(req InnoDBClusterStartScriptRequest) (string, error) {
	return installerkit.RenderTemplate("mysql", "innodb-cluster/start.sh", "mysql-innodb-cluster-start", innodbClusterStartScriptTemplate, mysqlScriptFuncs, req)
}

func renderDisasterRebuildScript(options DisasterRebuildScriptOptions) (string, error) {
	if err := validateDisasterRebuildScriptOptions(options); err != nil {
		return "", err
	}
	tpl, err := template.New("mysql-disaster-rebuild").Funcs(mysqlScriptFuncs).Parse(disasterRebuildScriptTemplate)
	if err != nil {
		return "", fmt.Errorf("parse fixed MySQL disaster rebuild template: %w", err)
	}
	var output bytes.Buffer
	if err := tpl.Execute(&output, options); err != nil {
		return "", fmt.Errorf("render fixed MySQL disaster rebuild template: %w", err)
	}
	return output.String(), nil
}

type InstallScriptRequest struct {
	Version     string
	WorkDir     string
	ArchivePath string
	InstallRoot string
	ReportHost  string
	Port        int
	ServerID    uint32
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

type InnoDBClusterStartRequest struct {
	ClusterName string
	InstallRoot string
	Connections []mysqlConnectionCredential
	Nodes       []InnoDBClusterNode
}

type InnoDBClusterBootstrapScriptRequest struct {
	ClusterName           string
	InstallRoot           string
	CredentialContextPath string
	Nodes                 []InnoDBClusterNode
}

type InnoDBClusterStartScriptRequest struct {
	ClusterName           string
	InstallRoot           string
	CredentialContextPath string
	Nodes                 []InnoDBClusterNode
}

type InnoDBClusterNode struct {
	Host string
	Port int
}
