package docker

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
	"aifar-deployment/backend/internal/i18n"
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

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, log Logger) error {
	return i.InstallWithLanguage(ctx, server, bundle, log, "")
}

func (i Installer) InstallWithLanguage(ctx context.Context, server store.Server, bundle Bundle, log Logger, lang string) error {
	if bundle.ArchivePath == "" {
		return errors.New(i18n.Text(lang, "docker.archiveRequired"))
	}
	deployDir := remoteDeployDir(server.DeployDir)
	workDir := path.Join(deployDir, "_work", fmt.Sprintf("docker-%s-%d", sanitize(bundle.Version), time.Now().Unix()))
	installRoot := path.Join(deployDir, "docker", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	log.Info(i18n.Text(lang, "docker.prepareWorkDir"), workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+shellQuote(workDir+"/rpms"), log, lang); err != nil {
		return err
	}
	log.Info(i18n.Text(lang, "docker.uploadArchive"), bundle.ArchivePath)
	if err := i.remote.UploadFile(ctx, server, bundle.ArchivePath, archiveRemote, 0o644); err != nil {
		return fmt.Errorf("%s: %w", i18n.Text(lang, "docker.uploadArchiveFailed"), err)
	}
	for _, rpm := range bundle.RPMPaths {
		remoteRPM := workDir + "/rpms/" + filepath.Base(rpm)
		log.Info(i18n.Text(lang, "docker.uploadRPM"), filepath.Base(rpm))
		if err := i.remote.UploadFile(ctx, server, rpm, remoteRPM, 0o644); err != nil {
			return fmt.Errorf("%s: %w", i18n.Text(lang, "docker.uploadRPMFailed", filepath.Base(rpm)), err)
		}
	}
	script, err := installScript(bundle.Version, workDir, archiveRemote, installRoot)
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-docker.sh"
	scriptLocal, err := writeTempScript(script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	log.Info(i18n.Text(lang, "docker.uploadScript"))
	if err := i.remote.UploadFile(ctx, server, scriptLocal, scriptRemote, 0o755); err != nil {
		return fmt.Errorf("%s: %w", i18n.Text(lang, "docker.uploadScriptFailed"), err)
	}
	log.Info(i18n.Text(lang, "docker.installing"))
	if _, err := i.run(ctx, server, "sh "+shellQuote(scriptRemote), log, lang); err != nil {
		return err
	}
	log.Info(i18n.Text(lang, "docker.installFinished"), bundle.Version)
	return nil
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger, lang string) (adapter.CommandResult, error) {
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
		return result, fmt.Errorf("%s: %w", i18n.Text(lang, "docker.remoteCommandFailed"), err)
	}
	return result, nil
}

func writeTempScript(script string) (string, error) {
	f, err := os.CreateTemp("", "aifar-docker-install-*.sh")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(script); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func sanitize(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "'", "")
	return replacer.Replace(value)
}

func remoteDeployDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/aifar/apps"
	}
	return "/" + strings.Trim(path.Clean(value), "/")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
