package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
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

type Bundle struct {
	Version     string
	ArchivePath string
	RPMPaths    []string
}

type Installer struct {
	remote Remote
}

func NewInstaller(remote Remote) Installer {
	return Installer{remote: remote}
}

func SelectBundle(resources []store.Resource, version string) (Bundle, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "latest"
	}
	candidates := make([]store.Resource, 0)
	for _, res := range resources {
		name := strings.ToLower(filepath.Base(res.Path))
		if res.App != "redis" || res.Part != "backend" {
			continue
		}
		if version != "latest" && res.Version != version {
			continue
		}
		if strings.HasSuffix(name, ".sha256sum") || strings.HasSuffix(name, ".minisig") {
			continue
		}
		if !strings.Contains(name, "redis") {
			continue
		}
		candidates = append(candidates, res)
	}
	if len(candidates) == 0 {
		return Bundle{}, fmt.Errorf("redis resource %s not found", version)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Version == candidates[j].Version {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].Version < candidates[j].Version
	})
	selected := candidates[len(candidates)-1]
	return Bundle{
		Version:     selected.Version,
		ArchivePath: selected.Path,
		RPMPaths:    discoverRPMs(selected.Path),
	}, nil
}

func VerifyBundle(bundle Bundle) error {
	if strings.TrimSpace(bundle.ArchivePath) == "" {
		return errors.New("redis archive is required")
	}
	if _, err := os.Stat(bundle.ArchivePath); err != nil {
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
	deployDir := remoteDeployDir(server.DeployDir)
	workDir := path.Join(deployDir, "_work", fmt.Sprintf("redis-%s-%d", sanitize(bundle.Version), time.Now().Unix()))
	installRoot := path.Join(deployDir, "redis", bundle.Version)
	archiveRemote := workDir + "/" + filepath.Base(bundle.ArchivePath)
	log.Info("prepare Redis work directory: %s", workDir)
	if _, err := i.run(ctx, server, "mkdir -p "+shellQuote(workDir+"/rpms"), log); err != nil {
		return err
	}
	log.Info("upload Redis archive: %s", bundle.ArchivePath)
	if err := i.remote.UploadFile(ctx, server, bundle.ArchivePath, archiveRemote, 0o644); err != nil {
		return fmt.Errorf("upload redis archive failed: %w", err)
	}
	for _, rpm := range bundle.RPMPaths {
		remoteRPM := workDir + "/rpms/" + filepath.Base(rpm)
		log.Info("upload Redis RPM dependency: %s", filepath.Base(rpm))
		if err := i.remote.UploadFile(ctx, server, rpm, remoteRPM, 0o644); err != nil {
			return fmt.Errorf("upload redis rpm %s failed: %w", filepath.Base(rpm), err)
		}
	}
	script, err := installStandaloneScript(bundle.Version, workDir, archiveRemote, installRoot, port, password)
	if err != nil {
		return err
	}
	scriptRemote := workDir + "/install-redis.sh"
	scriptLocal, err := writeTempScript(script)
	if err != nil {
		return err
	}
	defer os.Remove(scriptLocal)
	log.Info("upload Redis installer script")
	if err := i.remote.UploadFile(ctx, server, scriptLocal, scriptRemote, 0o755); err != nil {
		return fmt.Errorf("upload redis installer script failed: %w", err)
	}
	log.Info("install Redis standalone service")
	if _, err := i.run(ctx, server, "sh "+shellQuote(scriptRemote), log); err != nil {
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
		return result, fmt.Errorf("redis remote command failed: %w", err)
	}
	return result, nil
}

func writeTempScript(script string) (string, error) {
	f, err := os.CreateTemp("", "aifar-redis-install-*.sh")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(script); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func discoverRPMs(archivePath string) []string {
	rpmDir := filepath.Join(filepath.Dir(archivePath), "rpms")
	matches, err := filepath.Glob(filepath.Join(rpmDir, "*.rpm"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
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
