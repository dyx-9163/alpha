package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"aifar-deployment/backend/internal/i18n"
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

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, log Logger) error {
	return i.InstallWithLanguage(ctx, server, bundle, log, "")
}

func (i Installer) InstallWithLanguage(ctx context.Context, server store.Server, bundle Bundle, log Logger, lang string, opts ...InstallOptions) error {
	if bundle.ArchivePath == "" {
		return errors.New(i18n.Text(lang, "docker.archiveRequired"))
	}
	options := InstallOptions{}
	if len(opts) > 0 {
		options = opts[0]
	}
	options = NormalizeInstallOptions(options)
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, "docker", bundle.Version, time.Now())
	installRoot := installerkit.InstallRoot(deployDir, "docker")
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	log.Info(i18n.Text(lang, "docker.prepareWorkDir"), workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+installerkit.ShellQuote(workDir+"/rpms"), log, lang); err != nil {
		return err
	}
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      bundle.ArchivePath,
		RemotePath:     archiveRemote,
		LogMessage:     i18n.Text(lang, "docker.uploadArchive"),
		LogArgs:        []any{bundle.ArchivePath},
		FailureMessage: i18n.Text(lang, "docker.uploadArchiveFailed"),
	}, log); err != nil {
		return err
	}
	for _, file := range uploadkit.RPMFiles(bundle.RPMPaths, workDir+"/rpms", i18n.Text(lang, "docker.uploadRPM"), i18n.Text(lang, "docker.uploadRPMFailed")) {
		if err := uploadkit.Upload(ctx, i.remote, server, file, log); err != nil {
			return err
		}
	}
	script, err := installScript(bundle.Version, workDir, archiveRemote, installRoot, options)
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-docker.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-docker-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     i18n.Text(lang, "docker.uploadScript"),
		FailureMessage: i18n.Text(lang, "docker.uploadScriptFailed"),
	}, log); err != nil {
		return err
	}
	log.Info(i18n.Text(lang, "docker.installing"))
	if _, err := i.run(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), log, lang); err != nil {
		return err
	}
	log.Info(i18n.Text(lang, "docker.installFinished"), bundle.Version)
	return nil
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger, lang string) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, i18n.Text(lang, "docker.remoteCommandFailed"))
}
