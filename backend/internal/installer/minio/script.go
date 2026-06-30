package minio

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
)

//go:embed templates/standalone/install.sh
var standaloneInstallScriptTemplate string

//go:embed templates/standalone/uninstall.sh
var standaloneUninstallScriptTemplate string

//go:embed templates/distributed/configure-node.sh
var distributedConfigureScriptTemplate string

var minioScriptFuncs = template.FuncMap{
	"shq": installerkit.ShellQuote,
}

var minioStandaloneInstallTemplate = template.Must(template.New("minio-standalone-install").
	Funcs(minioScriptFuncs).
	Parse(standaloneInstallScriptTemplate))

var minioStandaloneUninstallTemplate = template.Must(template.New("minio-standalone-uninstall").
	Funcs(minioScriptFuncs).
	Parse(standaloneUninstallScriptTemplate))

var minioDistributedConfigureTemplate = template.Must(template.New("minio-distributed-configure").
	Funcs(minioScriptFuncs).
	Parse(distributedConfigureScriptTemplate))

func installStandaloneScript(req InstallScriptRequest) (string, error) {
	req = normalizeInstallScriptRequest(req)
	return renderMinIOScript(minioStandaloneInstallTemplate, req)
}

func uninstallStandaloneScript(version, installRoot string, apiPort int) (string, error) {
	return renderMinIOScript(minioStandaloneUninstallTemplate, UninstallScriptRequest{
		Version:     version,
		InstallRoot: installRoot,
		APIPort:     apiPort,
	})
}

func configureDistributedNodeScript(req DistributedNodeConfig) (string, error) {
	return renderMinIOScript(minioDistributedConfigureTemplate, req)
}

func renderMinIOScript(tpl *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func normalizeInstallScriptRequest(req InstallScriptRequest) InstallScriptRequest {
	req.DataDirs = minioVolumeDirs(req.DataDir, req.DataDirs, req.InstallRoot)
	req.DataDir = req.DataDirs[0]
	if strings.TrimSpace(req.VolumeList) == "" {
		req.VolumeList = strings.Join(req.DataDirs, " ")
	}
	return req
}

type InstallScriptRequest struct {
	Version        string
	WorkDir        string
	ArchivePath    string
	GoArchivePath  string
	GoModCachePath string
	MCRemotePath   string
	InstallRoot    string
	DataDir        string
	DataDirs       []string
	VolumeList     string
	APIPort        int
	ConsolePort    int
	RootUser       string
	RootPassword   string
}

type UninstallScriptRequest struct {
	Version     string
	InstallRoot string
	APIPort     int
}

type DistributedNodeConfig struct {
	Version      string
	InstallRoot  string
	APIPort      int
	ConsolePort  int
	RootUser     string
	RootPassword string
	DataDir      string
	Volumes      []DistributedVolume
}

type DistributedVolume struct {
	Host string
	Port int
	Path string
}
