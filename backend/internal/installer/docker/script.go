package docker

import (
	"bytes"
	_ "embed"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
)

//go:embed templates/install.sh
var installScriptTemplate string

//go:embed templates/uninstall.sh
var uninstallScriptTemplate string

var dockerScriptFuncs = template.FuncMap{
	"shq": installerkit.ShellQuote,
}

var dockerInstallTemplate = template.Must(template.New("docker-install").
	Funcs(dockerScriptFuncs).
	Parse(installScriptTemplate))

var dockerUninstallTemplate = template.Must(template.New("docker-uninstall").
	Funcs(dockerScriptFuncs).
	Parse(uninstallScriptTemplate))

type installScriptData struct {
	Version       string
	WorkDir       string
	ArchivePath   string
	InstallRoot   string
	BridgeCIDR    string
	RemoteAPIPort int
}

type uninstallScriptData struct {
	Version     string
	InstallRoot string
}

func installScript(version, workDir, archivePath, installRoot string, options ...InstallOptions) (string, error) {
	normalized := InstallOptions{}
	if len(options) > 0 {
		normalized = options[0]
	}
	normalized = NormalizeInstallOptions(normalized)
	return renderDockerScript(dockerInstallTemplate, installScriptData{
		Version:       version,
		WorkDir:       workDir,
		ArchivePath:   archivePath,
		InstallRoot:   installRoot,
		BridgeCIDR:    normalized.BridgeCIDR,
		RemoteAPIPort: normalized.RemoteAPIPort,
	})
}

func uninstallScript(version, installRoot string) (string, error) {
	return renderDockerScript(dockerUninstallTemplate, uninstallScriptData{
		Version:     version,
		InstallRoot: installRoot,
	})
}

func renderDockerScript(tpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
