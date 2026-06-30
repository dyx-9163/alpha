package mysqlrouter

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

type RouterInstallOptions struct {
	BasePort          int
	BootstrapHost     string
	BootstrapPort     int
	BootstrapUser     string
	BootstrapPassword string
	BindAddress       string
}

func NewInstaller(remote Remote) Installer {
	return Installer{remote: remote}
}

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, req RouterInstallOptions, log Logger) error {
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	if err := req.Validate(); err != nil {
		return err
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, "mysql-router", bundle.Version, time.Now())
	installRoot := installerkit.InstallRoot(deployDir, "mysql-router")
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)

	log.Info("prepare MySQL Router work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+installerkit.ShellQuote(workDir+"/rpms"), log); err != nil {
		return err
	}
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      bundle.ArchivePath,
		RemotePath:     archiveRemote,
		LogMessage:     "upload MySQL official bundle: %s",
		LogArgs:        []any{bundle.ArchivePath},
		FailureMessage: "upload mysql bundle failed",
	}, log); err != nil {
		return err
	}
	for _, file := range uploadkit.RPMFiles(bundle.RPMPaths, workDir+"/rpms", "upload MySQL RPM dependency: %s", "upload mysql rpm %s failed") {
		if err := uploadkit.Upload(ctx, i.remote, server, file, log); err != nil {
			return err
		}
	}

	bindAddress := strings.TrimSpace(req.BindAddress)
	if bindAddress == "" {
		bindAddress = "0.0.0.0"
	}
	script, err := installRouterScript(RouterInstallScriptRequest{
		Version:           bundle.Version,
		WorkDir:           workDir,
		ArchivePath:       archiveRemote,
		InstallRoot:       installRoot,
		BasePort:          req.BasePort,
		BootstrapHost:     req.BootstrapHost,
		BootstrapPort:     req.BootstrapPort,
		BootstrapUser:     req.BootstrapUser,
		BootstrapPassword: req.BootstrapPassword,
		BindAddress:       bindAddress,
	})
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-mysql-router.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-mysql-router-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     "upload MySQL Router installer script",
		FailureMessage: "upload mysql router installer script failed",
	}, log); err != nil {
		return err
	}
	log.Info("install MySQL Router service")
	if _, err := i.run(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), log); err != nil {
		return err
	}
	log.Info("MySQL Router %s installed and verified on base port %d", bundle.Version, req.BasePort)
	return nil
}

func (o RouterInstallOptions) Validate() error {
	if o.BasePort <= 0 || o.BasePort+3 > 65535 {
		return fmt.Errorf("invalid MySQL Router base port: %d", o.BasePort)
	}
	if strings.TrimSpace(o.BootstrapHost) == "" {
		return errors.New("MySQL Router bootstrap host is required")
	}
	if o.BootstrapPort <= 0 || o.BootstrapPort > 65535 {
		return fmt.Errorf("invalid MySQL Router bootstrap port: %d", o.BootstrapPort)
	}
	if strings.TrimSpace(o.BootstrapUser) == "" {
		return errors.New("MySQL Router bootstrap user is required")
	}
	if strings.TrimSpace(o.BootstrapPassword) == "" {
		return errors.New("MySQL Router bootstrap password is required")
	}
	if strings.IndexFunc(o.BootstrapUser, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MySQL Router bootstrap user must not contain whitespace")
	}
	if strings.IndexFunc(o.BootstrapPassword, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MySQL Router bootstrap password must not contain whitespace")
	}
	return nil
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, "mysql router remote command failed")
}
