package minio

import (
	_ "embed"
	"strings"
	"text/template"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
)

//go:embed templates/standalone/install.sh
var standaloneInstallScriptTemplate string

//go:embed templates/standalone/uninstall.sh
var standaloneUninstallScriptTemplate string

//go:embed templates/distributed/configure-node.sh
var distributedConfigureScriptTemplate string

var minioScriptFuncs = selinux.AddTemplateFuncs(template.FuncMap{
	"shq": installerkit.ShellQuote,
})

func installStandaloneScript(req InstallScriptRequest) (string, error) {
	req = normalizeInstallScriptRequest(req)
	return installerkit.RenderTemplate("minio", "standalone/install.sh", "minio-standalone-install", standaloneInstallScriptTemplate, minioScriptFuncs, req)
}

func uninstallStandaloneScript(version, installRoot, legacyInstallRoot string, apiPort int, options UninstallOptions) (string, error) {
	return installerkit.RenderTemplate("minio", "standalone/uninstall.sh", "minio-standalone-uninstall", standaloneUninstallScriptTemplate, minioScriptFuncs, UninstallScriptRequest{
		Version:            version,
		InstallRoot:        installRoot,
		LegacyInstallRoot:  legacyInstallRoot,
		APIPort:            apiPort,
		RemoveMountedDisks: options.RemoveMountedDisks,
		MountRoots:         cleanMountRoots(options.MountRoots),
	})
}

func configureDistributedNodeScript(req DistributedNodeConfig) (string, error) {
	return installerkit.RenderTemplate("minio", "distributed/configure-node.sh", "minio-distributed-configure", distributedConfigureScriptTemplate, minioScriptFuncs, req)
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
	Version            string
	InstallRoot        string
	LegacyInstallRoot  string
	APIPort            int
	RemoveMountedDisks bool
	MountRoots         []string
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
