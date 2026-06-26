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

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
}

type Remote interface {
	Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error)
	UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error
}

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
	deployDir := remoteDeployDir(server.DeployDir)
	workDir := path.Join(deployDir, "_work", fmt.Sprintf("minio-%s-%d", sanitize(bundle.Version), time.Now().Unix()))
	installRoot := path.Join(deployDir, "minio", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	goArchiveRemote := workDir + "/" + filepath.Base(bundle.GoArchivePath)
	goModCacheRemote := workDir + "/" + filepath.Base(bundle.GoModCachePath)
	mcRemote := ""
	if strings.TrimSpace(bundle.MCPath) != "" {
		mcRemote = workDir + "/" + filepath.Base(bundle.MCPath)
	}

	log.Info("prepare MinIO work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+shellQuote(workDir+"/rpms"), log); err != nil {
		return err
	}

	log.Info("upload MinIO source archive: %s", bundle.ArchivePath)
	if err := i.remote.UploadFile(ctx, server, bundle.ArchivePath, archiveRemote, 0o644); err != nil {
		return fmt.Errorf("upload minio archive failed: %w", err)
	}
	log.Info("upload Go toolchain archive: %s", bundle.GoArchivePath)
	if err := i.remote.UploadFile(ctx, server, bundle.GoArchivePath, goArchiveRemote, 0o644); err != nil {
		return fmt.Errorf("upload minio go toolchain failed: %w", err)
	}
	log.Info("upload Go module cache archive: %s", bundle.GoModCachePath)
	if err := i.remote.UploadFile(ctx, server, bundle.GoModCachePath, goModCacheRemote, 0o644); err != nil {
		return fmt.Errorf("upload minio go module cache failed: %w", err)
	}
	if mcRemote != "" {
		log.Info("upload MinIO client: %s", bundle.MCPath)
		if err := i.remote.UploadFile(ctx, server, bundle.MCPath, mcRemote, 0o755); err != nil {
			return fmt.Errorf("upload minio client failed: %w", err)
		}
	}
	for _, rpm := range bundle.RPMPaths {
		remoteRPM := workDir + "/rpms/" + filepath.Base(rpm)
		log.Info("upload MinIO RPM dependency: %s", filepath.Base(rpm))
		if err := i.remote.UploadFile(ctx, server, rpm, remoteRPM, 0o644); err != nil {
			return fmt.Errorf("upload minio rpm %s failed: %w", filepath.Base(rpm), err)
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
	scriptLocal, err := writeTempScript(script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	log.Info("upload MinIO installer script")
	if err := i.remote.UploadFile(ctx, server, scriptLocal, scriptRemote, 0o755); err != nil {
		return fmt.Errorf("upload minio installer script failed: %w", err)
	}
	log.Info("install MinIO standalone service")
	if _, err := i.run(ctx, server, "sh "+shellQuote(scriptRemote), log); err != nil {
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

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (adapter.CommandResult, error) {
	result, err := i.remote.Run(ctx, server, command)
	if strings.TrimSpace(result.Stdout) != "" {
		log.Info("%s", strings.TrimSpace(result.Stdout))
	}
	if strings.TrimSpace(result.Stderr) != "" {
		if err != nil {
			log.Error("%s", strings.TrimSpace(result.Stderr))
		} else {
			log.Info("%s", strings.TrimSpace(result.Stderr))
		}
	}
	if err != nil {
		return result, fmt.Errorf("minio remote command failed: %w", err)
	}
	return result, nil
}

func writeTempScript(script string) (string, error) {
	f, err := os.CreateTemp("", "aifar-minio-install-*.sh")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(script); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func remoteDeployDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/aifar/apps"
	}
	return "/" + strings.Trim(path.Clean(value), "/")
}

func sanitize(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "'", "")
	return replacer.Replace(value)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
