package mysqlrouter

import (
	_ "embed"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
)

//go:embed templates/install.sh
var routerInstallScriptTemplate string

//go:embed templates/uninstall.sh
var routerUninstallScriptTemplate string

var mysqlRouterScriptFuncs = template.FuncMap{
	"shq":                  installerkit.ShellQuote,
	"serviceAccessHelpers": installerkit.ServiceAccessHelpers,
}

func installRouterScript(req RouterInstallScriptRequest) (string, error) {
	return installerkit.RenderTemplate("mysql-router", "install.sh", "mysql-router-install", routerInstallScriptTemplate, mysqlRouterScriptFuncs, req)
}

func uninstallRouterScript(version, installRoot, legacyInstallRoot string, basePort int) (string, error) {
	return installerkit.RenderTemplate("mysql-router", "uninstall.sh", "mysql-router-uninstall", routerUninstallScriptTemplate, mysqlRouterScriptFuncs, RouterUninstallScriptRequest{
		Version:           version,
		InstallRoot:       installRoot,
		LegacyInstallRoot: legacyInstallRoot,
		BasePort:          basePort,
	})
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
	Version           string
	InstallRoot       string
	LegacyInstallRoot string
	BasePort          int
}
