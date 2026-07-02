package nacos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/store"
)

type Logger = installerkit.Logger
type Remote = installerkit.Remote

type Installer struct {
	remote Remote
}

func NewInstaller(remote Remote) Installer {
	return Installer{remote: remote}
}

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, req InstallOptions, nodes []NacosClusterNode, log Logger) error {
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	if err := req.Validate(); err != nil {
		return err
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, "nacos", bundle.Version, time.Now())
	installRoot := installerkit.InstallRoot(deployDir, "nacos")
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	jdkLocal, err := i.jdkForServer(ctx, server, bundle, log)
	if err != nil {
		return err
	}
	jdkRemote := workDir + "/" + filepath.Base(jdkLocal)

	log.Info("prepare Nacos work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+installerkit.ShellQuote(workDir), log); err != nil {
		return err
	}
	for _, file := range []uploadkit.File{
		{
			LocalPath:      bundle.ArchivePath,
			RemotePath:     archiveRemote,
			LogMessage:     "upload Nacos archive: %s",
			LogArgs:        []any{bundle.ArchivePath},
			FailureMessage: "upload nacos archive failed",
		},
		{
			LocalPath:      jdkLocal,
			RemotePath:     jdkRemote,
			LogMessage:     "upload Nacos JDK archive: %s",
			LogArgs:        []any{jdkLocal},
			FailureMessage: "upload nacos jdk archive failed",
		},
	} {
		if err := uploadkit.Upload(ctx, i.remote, server, file, log); err != nil {
			return err
		}
	}

	script, err := installNacosScript(InstallScriptRequest{
		Version:       bundle.Version,
		Mode:          req.Topology,
		WorkDir:       workDir,
		ArchivePath:   archiveRemote,
		JDKPath:       jdkRemote,
		InstallRoot:   installRoot,
		Port:          req.Port,
		GRPCPort:      req.GRPCPort,
		GRPCRaftPort:  req.GRPCRaftPort,
		RaftPort:      req.RaftPort,
		JVMXMS:        req.JVMXMS,
		JVMXMX:        req.JVMXMX,
		JVMXMN:        req.JVMXMN,
		AdminUser:     req.AdminUser,
		AdminPassword: req.AdminPassword,
		Database:      req.Database,
		ClusterNodes:  nodes,
	})
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-nacos.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-nacos-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     "upload Nacos installer script",
		FailureMessage: "upload nacos installer script failed",
	}, log); err != nil {
		return err
	}
	log.Info("install Nacos service")
	if _, err := i.run(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), log); err != nil {
		return err
	}
	log.Info("Nacos %s installed and verified on port %d", bundle.Version, req.Port)
	return nil
}

func (i Installer) Uninstall(ctx context.Context, server store.Server, version string, port int, log Logger) error {
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	script, err := uninstallNacosScript(UninstallScriptRequest{
		Version:           version,
		InstallRoot:       installerkit.InstallRoot(deployDir, "nacos"),
		LegacyInstallRoot: installerkit.LegacyInstallRoot(deployDir, "nacos", version),
		Port:              port,
	})
	if err != nil {
		return err
	}
	return i.runInlineScript(ctx, server, "AIFAR_NACOS_UNINSTALL", script, log)
}

func (i Installer) Check(ctx context.Context, server store.Server, port int, log Logger) error {
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	script, err := checkNacosScript(CheckScriptRequest{
		InstallRoot: installerkit.InstallRoot(deployDir, "nacos"),
		Port:        port,
	})
	if err != nil {
		return err
	}
	return i.runInlineScript(ctx, server, "AIFAR_NACOS_CHECK", script, log)
}

func (i Installer) jdkForServer(ctx context.Context, server store.Server, bundle Bundle, log Logger) (string, error) {
	result, err := i.run(ctx, server, "uname -m", log)
	if err != nil {
		return "", err
	}
	arch := strings.ToLower(strings.TrimSpace(result.Stdout))
	switch arch {
	case "x86_64", "amd64":
		return bundle.JDKX64Path, nil
	case "aarch64", "arm64":
		return bundle.JDKAarch64Path, nil
	default:
		return "", fmt.Errorf("unsupported Nacos target architecture: %s", arch)
	}
}

func (i Installer) runInlineScript(ctx context.Context, server store.Server, marker, script string, log Logger) error {
	if strings.TrimSpace(marker) == "" {
		return errors.New("inline script marker is required")
	}
	_, err := i.run(ctx, server, "sh -s <<'"+marker+"'\n"+script+"\n"+marker, log)
	return err
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, "nacos remote command failed")
}
