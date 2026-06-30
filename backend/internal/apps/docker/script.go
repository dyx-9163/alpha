package docker

import (
	_ "embed"
	"strings"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
)

//go:embed templates/install.sh
var installScriptTemplate string

//go:embed templates/uninstall.sh
var uninstallScriptTemplate string

var dockerScriptFuncs = selinux.AddTemplateFuncs(template.FuncMap{
	"shq": installerkit.ShellQuote,
})

type installScriptData struct {
	Version       string
	WorkDir       string
	ArchivePath   string
	InstallRoot   string
	BridgeCIDR    string
	RemoteAPIPort int
}

type uninstallScriptData struct {
	Version           string
	InstallRoot       string
	LegacyInstallRoot string
}

func installScript(version, workDir, archivePath, installRoot string, options ...InstallOptions) (string, error) {
	normalized := InstallOptions{}
	if len(options) > 0 {
		normalized = options[0]
	}
	normalized = NormalizeInstallOptions(normalized)
	return installerkit.RenderTemplate("docker", "install.sh", "docker-install", installScriptTemplate, dockerScriptFuncs, installScriptData{
		Version:       version,
		WorkDir:       workDir,
		ArchivePath:   archivePath,
		InstallRoot:   installRoot,
		BridgeCIDR:    normalized.BridgeCIDR,
		RemoteAPIPort: normalized.RemoteAPIPort,
	})
}

func uninstallScript(version, installRoot string) (string, error) {
	return installerkit.RenderTemplate("docker", "uninstall.sh", "docker-uninstall", uninstallScriptTemplate, dockerScriptFuncs, uninstallScriptData{
		Version:           version,
		InstallRoot:       installRoot,
		LegacyInstallRoot: installerkit.LegacyInstallRoot(pathDir(installRoot), "docker", version),
	})
}

func pathDir(value string) string {
	if value == "" {
		return ""
	}
	trimmed := strings.TrimRight(value, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx <= 0 {
		return "/"
	}
	return trimmed[:idx]
}
