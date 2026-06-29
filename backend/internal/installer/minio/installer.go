package minio

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
	workDir := installerkit.WorkDir(deployDir, "minio", bundle.Version, time.Now())
	installRoot := path.Join(deployDir, "minio", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	goArchiveRemote := workDir + "/" + filepath.Base(bundle.GoArchivePath)
	goModCacheRemote := workDir + "/" + filepath.Base(bundle.GoModCachePath)
	mcRemote := ""
	if strings.TrimSpace(bundle.MCPath) != "" {
		mcRemote = workDir + "/" + filepath.Base(bundle.MCPath)
	}

	log.Info("prepare MinIO work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+installerkit.ShellQuote(workDir+"/rpms"), log); err != nil {
		return err
	}

	uploads := []uploadkit.File{
		{
			LocalPath:      bundle.ArchivePath,
			RemotePath:     archiveRemote,
			LogMessage:     "upload MinIO source archive: %s",
			LogArgs:        []any{bundle.ArchivePath},
			FailureMessage: "upload minio archive failed",
		},
		{
			LocalPath:      bundle.GoArchivePath,
			RemotePath:     goArchiveRemote,
			LogMessage:     "upload Go toolchain archive: %s",
			LogArgs:        []any{bundle.GoArchivePath},
			FailureMessage: "upload minio go toolchain failed",
		},
		{
			LocalPath:      bundle.GoModCachePath,
			RemotePath:     goModCacheRemote,
			LogMessage:     "upload Go module cache archive: %s",
			LogArgs:        []any{bundle.GoModCachePath},
			FailureMessage: "upload minio go module cache failed",
		},
	}
	if mcRemote != "" {
		uploads = append(uploads, uploadkit.File{
			LocalPath:      bundle.MCPath,
			RemotePath:     mcRemote,
			Mode:           0o755,
			LogMessage:     "upload MinIO client: %s",
			LogArgs:        []any{bundle.MCPath},
			FailureMessage: "upload minio client failed",
		})
	}
	uploads = append(uploads, uploadkit.RPMFiles(bundle.RPMPaths, workDir+"/rpms", "upload MinIO RPM dependency: %s", "upload minio rpm %s failed")...)
	for _, file := range uploads {
		if err := uploadkit.Upload(ctx, i.remote, server, file, log); err != nil {
			return err
		}
	}

	script, err := installStandaloneScript(InstallScriptRequest{
		Version:        bundle.Version,
		WorkDir:        workDir,
		ArchivePath:    archiveRemote,
		GoArchivePath:  goArchiveRemote,
		GoModCachePath: goModCacheRemote,
		MCRemotePath:   mcRemote,
		InstallRoot:    installRoot,
		APIPort:        req.APIPort,
		ConsolePort:    req.ConsolePort,
		RootUser:       req.RootUser,
		RootPassword:   req.RootPassword,
	})
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-minio.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-minio-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     "upload MinIO installer script",
		FailureMessage: "upload minio installer script failed",
	}, log); err != nil {
		return err
	}
	log.Info("install MinIO standalone service")
	if _, err := i.run(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), log); err != nil {
		return err
	}
	log.Info("MinIO %s installed and verified on ports %d/%d", bundle.Version, req.APIPort, req.ConsolePort)
	return nil
}

func (i Installer) ConfigureDistributedNode(ctx context.Context, server store.Server, req DistributedNodeConfig, log Logger) error {
	script, err := configureDistributedNodeScript(req)
	if err != nil {
		return err
	}
	_, err = i.run(ctx, server, "sh -s <<'AIFAR_MINIO_DISTRIBUTED_CONFIGURE'\n"+script+"\nAIFAR_MINIO_DISTRIBUTED_CONFIGURE", log)
	return err
}

type InstallOptions struct {
	APIPort      int
	ConsolePort  int
	RootUser     string
	RootPassword string
}

func (o InstallOptions) Validate() error {
	if o.APIPort <= 0 || o.APIPort > 65535 {
		return fmt.Errorf("invalid MinIO API port: %d", o.APIPort)
	}
	if o.ConsolePort <= 0 || o.ConsolePort > 65535 {
		return fmt.Errorf("invalid MinIO console port: %d", o.ConsolePort)
	}
	if o.APIPort == o.ConsolePort {
		return errors.New("MinIO API port and console port must be different")
	}
	if strings.TrimSpace(o.RootUser) == "" {
		return errors.New("MinIO root user is required")
	}
	if strings.TrimSpace(o.RootPassword) == "" {
		return errors.New("MinIO root password is required")
	}
	if strings.IndexFunc(o.RootUser, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MinIO root user must not contain whitespace")
	}
	if strings.IndexFunc(o.RootPassword, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MinIO root password must not contain whitespace")
	}
	if len(o.RootPassword) < 8 {
		return errors.New("MinIO root password must be at least 8 characters")
	}
	return nil
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, "minio remote command failed")
}
