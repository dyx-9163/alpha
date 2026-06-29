package mysql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
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

type InstallOptions struct {
	Port         int
	RootUser     string
	RootPassword string
}

func NewInstaller(remote Remote) Installer {
	return Installer{remote: remote}
}

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, req InstallOptions, log Logger) error {
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	if err := req.Validate(); err != nil {
		return err
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, "mysql", bundle.Version, time.Now())
	installRoot := path.Join(deployDir, "mysql", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)

	log.Info("prepare MySQL work directory: %s", workDir)
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

	script, err := installStandaloneScript(InstallScriptRequest{
		Version:      bundle.Version,
		WorkDir:      workDir,
		ArchivePath:  archiveRemote,
		InstallRoot:  installRoot,
		ReportHost:   strings.TrimSpace(server.Host),
		Port:         req.Port,
		RootUser:     req.RootUser,
		RootPassword: req.RootPassword,
	})
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-mysql.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-mysql-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     "upload MySQL installer script",
		FailureMessage: "upload mysql installer script failed",
	}, log); err != nil {
		return err
	}
	log.Info("install MySQL standalone service")
	if _, err := i.run(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), log); err != nil {
		return err
	}
	log.Info("MySQL %s installed and verified on port %d", bundle.Version, req.Port)
	return nil
}

func (i Installer) BootstrapInnoDBCluster(ctx context.Context, server store.Server, req InnoDBClusterBootstrapRequest, log Logger) error {
	script, err := bootstrapInnoDBClusterScript(req)
	if err != nil {
		return err
	}
	_, err = i.run(ctx, server, "sh -s <<'AIFAR_MYSQL_INNODB_CLUSTER'\n"+script+"\nAIFAR_MYSQL_INNODB_CLUSTER", log)
	return err
}

func (o InstallOptions) Validate() error {
	if o.Port <= 0 || o.Port > 65535 {
		return fmt.Errorf("invalid MySQL port: %d", o.Port)
	}
	if strings.TrimSpace(o.RootUser) == "" {
		return errors.New("MySQL root user is required")
	}
	if strings.TrimSpace(o.RootPassword) == "" {
		return errors.New("MySQL root password is required")
	}
	if strings.IndexFunc(o.RootUser, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MySQL root user must not contain whitespace")
	}
	if strings.IndexFunc(o.RootPassword, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MySQL root password must not contain whitespace")
	}
	if len(o.RootPassword) < 8 {
		return errors.New("MySQL root password must be at least 8 characters")
	}
	return nil
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, "mysql remote command failed")
}
