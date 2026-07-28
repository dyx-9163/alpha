package installerkit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type Logger interface {
	Info(format string, args ...any)
	Error(format string, args ...any)
}

type CommandResult = adapter.CommandResult

type DownloadResult = adapter.DownloadResult

type Remote interface {
	Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error)
	UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error
}

type FileDownloader interface {
	DownloadFile(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode) (DownloadResult, error)
}

const TemplateDirEnv = "AIFAR_INSTALLER_TEMPLATE_DIR"

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

func RenderTemplate(app, templatePath, name, embedded string, funcs template.FuncMap, data any) (string, error) {
	source, err := TemplateSource(app, templatePath, embedded)
	if err != nil {
		return "", err
	}
	tpl, err := template.New(name).Funcs(funcs).Parse(source)
	if err != nil {
		return "", fmt.Errorf("parse installer template %s/%s: %w", app, templatePath, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render installer template %s/%s: %w", app, templatePath, err)
	}
	return buf.String(), nil
}

func TemplateSource(app, templatePath, embedded string) (string, error) {
	root := strings.TrimSpace(os.Getenv(TemplateDirEnv))
	if root == "" {
		root = filepath.Join("config", "installers")
	}
	cleanPath := filepath.Clean(filepath.FromSlash(templatePath))
	if cleanPath == "." || filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || cleanPath == ".." {
		return "", fmt.Errorf("invalid installer template path: %s", templatePath)
	}
	overridePath := filepath.Join(root, Sanitize(app), cleanPath)
	content, err := os.ReadFile(overridePath)
	if err == nil {
		return string(content), nil
	}
	if os.IsNotExist(err) {
		return embedded, nil
	}
	return "", fmt.Errorf("read installer template override %s: %w", overridePath, err)
}

func WorkDir(deployDir, app, version string, now time.Time) string {
	return path.Join(RemoteDeployDir(deployDir), "_work", fmt.Sprintf("%s-%s-%d", Sanitize(app), Sanitize(version), now.Unix()))
}

func InstallRoot(deployDir, app string) string {
	return path.Join(RemoteDeployDir(deployDir), Sanitize(app))
}

func LegacyInstallRoot(deployDir, app, version string) string {
	return path.Join(RemoteDeployDir(deployDir), Sanitize(app), Sanitize(version))
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
