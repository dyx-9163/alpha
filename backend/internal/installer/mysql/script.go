package mysql

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed templates/standalone/install.sh
var standaloneInstallScriptTemplate string

//go:embed templates/standalone/uninstall.sh
var standaloneUninstallScriptTemplate string

//go:embed templates/innodb-cluster/bootstrap.sh
var innodbClusterBootstrapScriptTemplate string

var mysqlScriptFuncs = template.FuncMap{
	"shq": shellQuote,
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
	Port         int
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
	RootUser     string
	RootPassword string
	Nodes        []InnoDBClusterNode
}

type InnoDBClusterNode struct {
	Host string
	Port int
}
