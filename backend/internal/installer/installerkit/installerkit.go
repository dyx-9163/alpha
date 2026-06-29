package installerkit

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
}

type CommandResult = adapter.CommandResult

type Remote interface {
	Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error)
	UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error
}

func Run(ctx context.Context, remote Remote, server store.Server, command string, log Logger, failurePrefix string) (adapter.CommandResult, error) {
	result, err := remote.Run(ctx, server, command)
	LogCommandResult(result, err, log)
	if err != nil {
		if strings.TrimSpace(failurePrefix) == "" {
			failurePrefix = "remote command failed"
		}
		return result, fmt.Errorf("%s: %w", failurePrefix, err)
	}
	return result, nil
}

func LogCommandResult(result adapter.CommandResult, commandErr error, log Logger) {
	if log == nil {
		return
	}
	if strings.TrimSpace(result.Stdout) != "" {
		log.Info("%s", strings.TrimSpace(result.Stdout))
	}
	if strings.TrimSpace(result.Stderr) == "" {
		return
	}
	if commandErr != nil {
		log.Error("%s", strings.TrimSpace(result.Stderr))
		return
	}
	log.Info("%s", strings.TrimSpace(result.Stderr))
}

func WriteTempScript(pattern, script string) (string, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = "aifar-install-*.sh"
	}
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(script); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func WorkDir(deployDir, app, version string, now time.Time) string {
	return path.Join(RemoteDeployDir(deployDir), "_work", fmt.Sprintf("%s-%s-%d", Sanitize(app), Sanitize(version), now.Unix()))
}

func RemoteDeployDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/aifar/apps"
	}
	return "/" + strings.Trim(path.Clean(value), "/")
}

func Sanitize(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "'", "")
	return replacer.Replace(value)
}

func ShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
