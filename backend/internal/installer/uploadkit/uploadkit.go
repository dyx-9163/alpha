package uploadkit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
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

var (
	ErrChecksumMismatch   = errors.New("uploaded file checksum mismatch")
	ErrVerificationFailed = errors.New("uploaded file verification failed")
)

type VerifiedFile struct {
	File
	StageRoot              string
	VerificationLogMessage string
	VerificationLogArgs    []any
}

type Verification struct {
	SHA256 string
	Size   int64
}

func UploadVerified(ctx context.Context, remote installerkit.Remote, server store.Server, file VerifiedFile, log installerkit.Logger) (Verification, error) {
	if err := validateVerifiedRemotePath(file.StageRoot, file.RemotePath); err != nil {
		return Verification{}, fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}
	expectedSHA, expectedSize, err := localSHA256(file.LocalPath)
	if err != nil {
		return Verification{}, fmt.Errorf("%w: calculate local checksum: %v", ErrVerificationFailed, err)
	}
	if err := Upload(ctx, remote, server, file.File, log); err != nil {
		return Verification{}, err
	}
	result, runErr := remote.Run(ctx, server, verificationCommand(file.StageRoot, file.RemotePath, expectedSHA, expectedSize))
	installerkit.LogCommandResult(result, runErr, log)
	proof, proofErr := parseVerificationMarker(result.Stdout)
	if proofErr != nil {
		return Verification{}, fmt.Errorf("%w: target did not return a valid proof", ErrVerificationFailed)
	}
	if proof.SHA256 != expectedSHA || proof.Size != expectedSize {
		return Verification{}, fmt.Errorf("%w", ErrChecksumMismatch)
	}
	if runErr != nil {
		return Verification{}, fmt.Errorf("%w: target verification command failed", ErrVerificationFailed)
	}
	if log != nil && strings.TrimSpace(file.VerificationLogMessage) != "" {
		log.Info(file.VerificationLogMessage, file.VerificationLogArgs...)
	}
	return proof, nil
}

func localSHA256(pathname string) (string, int64, error) {
	f, err := os.Open(pathname)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), size, nil
}

func validateVerifiedRemotePath(stageRoot, remotePath string) error {
	if !path.IsAbs(stageRoot) || !path.IsAbs(remotePath) {
		return errors.New("staging paths must be absolute POSIX paths")
	}
	root := path.Clean(stageRoot)
	target := path.Clean(remotePath)
	if root == "/" {
		return errors.New("staging root cannot be /")
	}
	if target == root || !strings.HasPrefix(target, root+"/") {
		return errors.New("uploaded file must be a strict descendant of staging root")
	}
	return nil
}

var verificationMarker = regexp.MustCompile(`^AIFAR_UPLOAD_VERIFY ([a-f0-9]{64}) ([0-9]+)$`)

func parseVerificationMarker(stdout string) (Verification, error) {
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != 1 {
		return Verification{}, errors.New("invalid verification marker")
	}
	matches := verificationMarker.FindStringSubmatch(lines[0])
	if matches == nil {
		return Verification{}, errors.New("invalid verification marker")
	}
	size, err := strconv.ParseUint(matches[2], 10, 63)
	if err != nil || size > math.MaxInt64 {
		return Verification{}, errors.New("invalid verification size")
	}
	return Verification{SHA256: matches[1], Size: int64(size)}, nil
}

func verificationCommand(stageRoot, remotePath, expectedSHA string, expectedSize int64) string {
	return "set -eu\n" +
		"target=" + installerkit.ShellQuote(path.Clean(remotePath)) + "\n" +
		"stage_root=" + installerkit.ShellQuote(path.Clean(stageRoot)) + "\n" +
		"expected_sha256=" + installerkit.ShellQuote(expectedSHA) + "\n" +
		"expected_size=" + installerkit.ShellQuote(strconv.FormatInt(expectedSize, 10)) + "\n" +
		"case \"$target\" in\n" +
		"  \"$stage_root\"/*) ;;\n" +
		"  *) echo \"uploaded file escaped staging root\" >&2; exit 64 ;;\n" +
		"esac\n" +
		"actual_sha256=\"$(sha256sum -- \"$target\" | awk '{print $1}')\"\n" +
		"actual_size=\"$(stat -c %s -- \"$target\")\"\n" +
		"printf 'AIFAR_UPLOAD_VERIFY %s %s\\n' \"$actual_sha256\" \"$actual_size\"\n" +
		"if [ \"$actual_sha256\" != \"$expected_sha256\" ] || [ \"$actual_size\" != \"$expected_size\" ]; then\n" +
		"  rm -f -- \"$target\"\n" +
		"  exit 65\n" +
		"fi\n"
}

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
