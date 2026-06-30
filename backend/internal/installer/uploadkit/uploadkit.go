package uploadkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type File struct {
	LocalPath      string
	RemotePath     string
	Mode           os.FileMode
	MaxAttempts    int
	LogMessage     string
	LogArgs        []any
	FailureMessage string
	FailureArgs    []any
}

var uploadRetryDelay = 2 * time.Second

func Upload(ctx context.Context, remote installerkit.Remote, server store.Server, file File, log installerkit.Logger) error {
	if file.Mode == 0 {
		file.Mode = 0o644
	}
	maxAttempts := file.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	if log != nil && strings.TrimSpace(file.LogMessage) != "" {
		log.Info(file.LogMessage, file.LogArgs...)
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := remote.UploadFile(ctx, server, file.LocalPath, file.RemotePath, file.Mode)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == maxAttempts || !isRetryableUploadError(err) || ctx.Err() != nil {
			break
		}
		if log != nil {
			log.Info("upload failed, retrying (%d/%d): %v", attempt+1, maxAttempts, err)
		}
		if err := sleepContext(ctx, uploadRetryDelay); err != nil {
			lastErr = err
			break
		}
	}
	msg := format(file.FailureMessage, file.FailureArgs...)
	if strings.TrimSpace(msg) == "" {
		msg = "upload file failed"
	}
	return fmt.Errorf("%s: %w", msg, lastErr)
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

func isRetryableUploadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "no space left") ||
		strings.Contains(msg, "not enough space") ||
		strings.Contains(msg, "read-only file system") ||
		strings.Contains(msg, "not a directory") ||
		strings.Contains(msg, "no such file or directory") {
		return false
	}
	for _, token := range []string{
		"eof",
		"broken pipe",
		"connection reset",
		"connection timed out",
		"connection refused",
		"connection aborted",
		"i/o timeout",
		"use of closed network connection",
		"client connection lost",
		"ssh: disconnect",
		"ssh: unexpected packet",
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
