package uploadkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type File struct {
	LocalPath      string
	RemotePath     string
	Mode           os.FileMode
	LogMessage     string
	LogArgs        []any
	FailureMessage string
	FailureArgs    []any
}

func Upload(ctx context.Context, remote installerkit.Remote, server store.Server, file File, log installerkit.Logger) error {
	if file.Mode == 0 {
		file.Mode = 0o644
	}
	if log != nil && strings.TrimSpace(file.LogMessage) != "" {
		log.Info(file.LogMessage, file.LogArgs...)
	}
	if err := remote.UploadFile(ctx, server, file.LocalPath, file.RemotePath, file.Mode); err != nil {
		msg := format(file.FailureMessage, file.FailureArgs...)
		if strings.TrimSpace(msg) == "" {
			msg = "upload file failed"
		}
		return fmt.Errorf("%s: %w", msg, err)
	}
	return nil
}

func RPMFiles(paths []string, remoteDir, logMessage, failureMessage string) []File {
	out := make([]File, 0, len(paths))
	remoteDir = strings.TrimRight(remoteDir, "/")
	for _, path := range paths {
		base := filepath.Base(path)
		out = append(out, File{
			LocalPath:      path,
			RemotePath:     remoteDir + "/" + base,
			Mode:           0o644,
			LogMessage:     logMessage,
			LogArgs:        []any{base},
			FailureMessage: failureMessage,
			FailureArgs:    []any{base},
		})
	}
	return out
}

func format(message string, args ...any) string {
	if strings.TrimSpace(message) == "" {
		return ""
	}
	if len(args) == 0 {
		return message
	}
	return fmt.Sprintf(message, args...)
}
