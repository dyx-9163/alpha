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
	deployDir := remoteDeployDir(server.DeployDir)
	workDir := path.Join(deployDir, "_work", fmt.Sprintf("mysql-%s-%d", sanitize(bundle.Version), time.Now().Unix()))
	installRoot := path.Join(deployDir, "mysql", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)

	log.Info("prepare MySQL work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+shellQuote(workDir+"/rpms"), log); err != nil {
		return err
	}

	log.Info("upload MySQL official bundle: %s", bundle.ArchivePath)
	if err := i.remote.UploadFile(ctx, server, bundle.ArchivePath, archiveRemote, 0o644); err != nil {
		return fmt.Errorf("upload mysql bundle failed: %w", err)
	}
	for _, rpm := range bundle.RPMPaths {
		remoteRPM := workDir + "/rpms/" + filepath.Base(rpm)
		log.Info("upload MySQL RPM dependency: %s", filepath.Base(rpm))
		if err := i.remote.UploadFile(ctx, server, rpm, remoteRPM, 0o644); err != nil {
			return fmt.Errorf("upload mysql rpm %s failed: %w", filepath.Base(rpm), err)
		}
	}

	script, err := installStandaloneScript(InstallScriptRequest{
		Version:      bundle.Version,
		WorkDir:      workDir,
		ArchivePath:  archiveRemote,
		InstallRoot:  installRoot,
		Port:         req.Port,
		RootUser:     req.RootUser,
		RootPassword: req.RootPassword,
	})
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-mysql.sh"
	scriptLocal, err := writeTempScript(script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	log.Info("upload MySQL installer script")
	if err := i.remote.UploadFile(ctx, server, scriptLocal, scriptRemote, 0o755); err != nil {
		return fmt.Errorf("upload mysql installer script failed: %w", err)
	}
	log.Info("install MySQL standalone service")
	if _, err := i.run(ctx, server, "sh "+shellQuote(scriptRemote), log); err != nil {
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
		return result, fmt.Errorf("mysql remote command failed: %w", err)
	}
	return result, nil
}

func writeTempScript(script string) (string, error) {
	f, err := os.CreateTemp("", "aifar-mysql-install-*.sh")
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
