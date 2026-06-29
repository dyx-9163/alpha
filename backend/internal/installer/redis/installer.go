package redis

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
	"aifar-deployment/backend/internal/resourcekit"
	"aifar-deployment/backend/internal/store"
)

type Logger = installerkit.Logger

type Remote = installerkit.Remote

type Bundle struct {
	Version       string
	ArchivePath   string
	ArchiveSHA256 string
	RPMPaths      []string
}

type Installer struct {
	remote Remote
}

func NewInstaller(remote Remote) Installer {
	return Installer{remote: remote}
}

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	selected, ok := resourcekit.Select(resources, resourcekit.SelectOptions{
		App:            "redis",
		Part:           "backend",
		Version:        version,
		SkipSignatures: true,
		Match: func(baseLower string, _ store.Resource) bool {
			return strings.Contains(baseLower, "redis")
		},
	})
	if !ok {
		version = resourcekit.NormalizeVersion(version)
		return Bundle{}, fmt.Errorf("redis resource %s not found", version)
	}
	return Bundle{
		Version:       selected.Version,
		ArchivePath:   selected.Path,
		ArchiveSHA256: selected.SHA256,
		RPMPaths:      resourcekit.ListRPMs(filepath.Join(filepath.Dir(selected.Path), "rpms")),
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	if err := resourcekit.VerifyFile(bundle.ArchivePath, "redis archive"); err != nil {
		return err
	}
	if err := resourcekit.VerifySHA256(bundle.ArchivePath, bundle.ArchiveSHA256, "redis archive"); err != nil {
		return err
	}
	return nil
}

func (i Installer) Install(ctx context.Context, server store.Server, bundle Bundle, port int, password string, log Logger) error {
	return i.InstallWithLanguage(ctx, server, bundle, port, password, log, "")
}

func (i Installer) InstallWithLanguage(ctx context.Context, server store.Server, bundle Bundle, port int, password string, log Logger, lang string) error {
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return errors.New("redis password is required")
	}
	if strings.IndexFunc(password, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("redis password must not contain whitespace")
	}
	if port <= 0 {
		port = 6379
	}
	if port > 65535 {
		return fmt.Errorf("invalid redis port: %d", port)
	}
	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	workDir := installerkit.WorkDir(deployDir, "redis", bundle.Version, time.Now())
	installRoot := path.Join(deployDir, "redis", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	log.Info("prepare Redis work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+installerkit.ShellQuote(workDir+"/rpms"), log); err != nil {
		return err
	}
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      bundle.ArchivePath,
		RemotePath:     archiveRemote,
		LogMessage:     "upload Redis archive: %s",
		LogArgs:        []any{bundle.ArchivePath},
		FailureMessage: "upload redis archive failed",
	}, log); err != nil {
		return err
	}
	for _, file := range uploadkit.RPMFiles(bundle.RPMPaths, workDir+"/rpms", "upload Redis RPM dependency: %s", "upload redis rpm %s failed") {
		if err := uploadkit.Upload(ctx, i.remote, server, file, log); err != nil {
			return err
		}
	}
	script, err := installStandaloneScript(bundle.Version, workDir, archiveRemote, installRoot, port, password)
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-redis.sh"
	scriptLocal, err := installerkit.WriteTempScript("aifar-redis-install-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
		LocalPath:      scriptLocal,
		RemotePath:     scriptRemote,
		Mode:           0o755,
		LogMessage:     "upload Redis installer script",
		FailureMessage: "upload redis installer script failed",
	}, log); err != nil {
		return err
	}
	log.Info("install Redis standalone service")
	if _, err := i.run(ctx, server, "sh "+installerkit.ShellQuote(scriptRemote), log); err != nil {
		return err
	}
	log.Info("Redis %s installed and verified on port %d", bundle.Version, port)
	return nil
}

func (i Installer) ConfigureSentinelNode(ctx context.Context, server store.Server, req SentinelNodeConfig, log Logger) error {
	script, err := configureSentinelNodeScript(req)
	if err != nil {
		return err
	}
	return i.runInlineScript(ctx, server, "AIFAR_REDIS_SENTINEL_CONFIGURE", script, log)
}

func (i Installer) EnableClusterNode(ctx context.Context, server store.Server, req ClusterNodeConfig, log Logger) error {
	script, err := enableClusterNodeScript(req)
	if err != nil {
		return err
	}
	return i.runInlineScript(ctx, server, "AIFAR_REDIS_CLUSTER_ENABLE", script, log)
}

func (i Installer) BootstrapCluster(ctx context.Context, server store.Server, req ClusterBootstrapConfig, log Logger) error {
	script, err := bootstrapClusterScript(req)
	if err != nil {
		return err
	}
	return i.runInlineScript(ctx, server, "AIFAR_REDIS_CLUSTER_BOOTSTRAP", script, log)
}

func (i Installer) runInlineScript(ctx context.Context, server store.Server, marker, script string, log Logger) error {
	_, err := i.run(ctx, server, "sh -s <<'"+marker+"'\n"+script+"\n"+marker, log)
	return err
}

func (i Installer) run(ctx context.Context, server store.Server, command string, log Logger) (installerkit.CommandResult, error) {
	return installerkit.Run(ctx, i.remote, server, command, log, "redis remote command failed")
}
