package mysql

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

//go:embed templates/innodb-cluster/bootstrap.sh
var innodbClusterBootstrapScriptTemplate string

//go:embed templates/router/install.sh
var routerInstallScriptTemplate string

//go:embed templates/router/uninstall.sh
var routerUninstallScriptTemplate string

var mysqlScriptFuncs = template.FuncMap{
	"shq": installerkit.ShellQuote,
}

var mysqlStandaloneInstallTemplate = template.Must(template.New("mysql-standalone-install").
	Funcs(mysqlScriptFuncs).
	Parse(standaloneInstallScriptTemplate))

var mysqlStandaloneUninstallTemplate = template.Must(template.New("mysql-standalone-uninstall").
	Funcs(mysqlScriptFuncs).
	Parse(standaloneUninstallScriptTemplate))

var mysqlInnoDBClusterBootstrapTemplate = template.Must(template.New("mysql-innodb-cluster-bootstrap").
	Funcs(mysqlScriptFuncs).
	Parse(innodbClusterBootstrapScriptTemplate))

var mysqlRouterInstallTemplate = template.Must(template.New("mysql-router-install").
	Funcs(mysqlScriptFuncs).
	Parse(routerInstallScriptTemplate))

var mysqlRouterUninstallTemplate = template.Must(template.New("mysql-router-uninstall").
	Funcs(mysqlScriptFuncs).
	Parse(routerUninstallScriptTemplate))

func installStandaloneScript(req InstallScriptRequest) (string, error) {
	return renderMySQLScript(mysqlStandaloneInstallTemplate, req)
}

func uninstallStandaloneScript(version, installRoot string, port int) (string, error) {
	return renderMySQLScript(mysqlStandaloneUninstallTemplate, UninstallScriptRequest{
		Version:     version,
		InstallRoot: installRoot,
		Port:        port,
	})
}

func bootstrapInnoDBClusterScript(req InnoDBClusterBootstrapRequest) (string, error) {
	return renderMySQLScript(mysqlInnoDBClusterBootstrapTemplate, req)
}

func installRouterScript(req RouterInstallScriptRequest) (string, error) {
	return renderMySQLScript(mysqlRouterInstallTemplate, req)
}

func uninstallRouterScript(version, installRoot string, basePort int) (string, error) {
	return renderMySQLScript(mysqlRouterUninstallTemplate, RouterUninstallScriptRequest{
		Version:     version,
		InstallRoot: installRoot,
		BasePort:    basePort,
	})
}

func renderMySQLScript(tpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
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
	Version     string
	InstallRoot string
	Port        int
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

type RouterInstallScriptRequest struct {
	Version           string
	WorkDir           string
	ArchivePath       string
	InstallRoot       string
	BasePort          int
	BootstrapHost     string
	BootstrapPort     int
	BootstrapUser     string
	BootstrapPassword string
	BindAddress       string
}

type RouterUninstallScriptRequest struct {
	Version     string
	InstallRoot string
	BasePort    int
}
