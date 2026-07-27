package adapter

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aifar-deployment/backend/internal/store"

	"golang.org/x/crypto/ssh"
)

const (
	mysqlBackupRemoteRoot  = "/aifar/apps/mysql/_backup"
	mysqlBackupArchiveName = "dump.tar"
	maxDownloadStderrBytes = 4 << 10
)

const remoteDownloadHelper = `import os
import stat
import sys

archive_path = sys.argv[1]
archive_fd = os.open(archive_path, os.O_RDONLY | os.O_NOFOLLOW)
try:
    archive_stat = os.fstat(archive_fd)
    descriptor_path = os.path.realpath("/proc/self/fd/%d" % archive_fd)
    if not stat.S_ISREG(archive_stat.st_mode) or descriptor_path != archive_path:
        raise OSError("unsafe archive source")
    while True:
        chunk = os.read(archive_fd, 65536)
        if not chunk:
            break
        pending = memoryview(chunk)
        while pending:
            written = os.write(1, pending)
            if written <= 0:
                raise OSError("archive stream write failed")
            pending = pending[written:]
finally:
    os.close(archive_fd)
`

var mysqlBackupTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type DownloadResult struct {
	Size   int64
	SHA256 string
}

type sshDownloadClient interface {
	NewSession() (sshDownloadSession, error)
	Close() error
}

type sshDownloadSession interface {
	StdoutPipe() (io.Reader, error)
	SetStderr(*boundedDownloadBuffer)
	Start(string) error
	Wait() error
	Signal(ssh.Signal) error
	Close() error
}

type realSSHDownloadClient struct {
	client *ssh.Client
}

func (c *realSSHDownloadClient) NewSession() (sshDownloadSession, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, err
	}
	return &realSSHDownloadSession{session: session}, nil
}

func (c *realSSHDownloadClient) Close() error {
	return c.client.Close()
}

type realSSHDownloadSession struct {
	session *ssh.Session
}

func (s *realSSHDownloadSession) StdoutPipe() (io.Reader, error) {
	return s.session.StdoutPipe()
}

func (s *realSSHDownloadSession) SetStderr(dst *boundedDownloadBuffer) {
	s.session.Stderr = dst
}

func (s *realSSHDownloadSession) Start(command string) error {
	return s.session.Start(command)
}

func (s *realSSHDownloadSession) Wait() error {
	return s.session.Wait()
}

func (s *realSSHDownloadSession) Signal(signal ssh.Signal) error {
	return s.session.Signal(signal)
}

func (s *realSSHDownloadSession) Close() error {
	return s.session.Close()
}

type boundedDownloadBuffer struct {
	data []byte
}

func (b *boundedDownloadBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := maxDownloadStderrBytes - len(b.data)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return written, nil
}

func (b *boundedDownloadBuffer) String() string {
	return string(b.data)
}

func DownloadSSHFile(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode) (DownloadResult, error) {
	return downloadSSHFileWithDialer(ctx, server, remotePath, localPath, mode, func(ctx context.Context, server store.Server) (sshDownloadClient, error) {
		client, err := dialSSH(ctx, server)
		if err != nil {
			return nil, err
		}
		return &realSSHDownloadClient{client: client}, nil
	})
}

func downloadSSHFileWithDialer(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode, dial func(context.Context, store.Server) (sshDownloadClient, error)) (result DownloadResult, retErr error) {
	command, err := downloadCommand(remotePath)
	if err != nil {
		return DownloadResult{}, downloadOperationError(server.ID, "validate remote path", err)
	}
	if !filepath.IsAbs(localPath) || filepath.Clean(localPath) != localPath || filepath.Ext(localPath) != ".partial" {
		return DownloadResult{}, downloadOperationError(server.ID, "validate local partial", errors.New("invalid local partial path"))
	}
	if err := ctx.Err(); err != nil {
		return DownloadResult{}, downloadOperationError(server.ID, "create local partial", err)
	}

	file, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return DownloadResult{}, downloadOperationError(server.ID, "create local partial", err)
	}
	createdInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return DownloadResult{}, downloadOperationError(server.ID, "inspect local partial", statErr)
	}
	created := true
	defer func() {
		if !created || retErr == nil {
			return
		}
		if cleanupErr := removeCreatedDownload(localPath, createdInfo); cleanupErr != nil {
			retErr = errors.Join(retErr, downloadOperationError(server.ID, "clean local partial", cleanupErr))
		}
	}()
	if err := file.Chmod(mode.Perm()); err != nil {
		_ = file.Close()
		return DownloadResult{}, downloadOperationError(server.ID, "set local partial mode", err)
	}

	client, err := dial(ctx, server)
	if err != nil {
		_ = file.Close()
		return DownloadResult{}, downloadOperationError(server.ID, "connect SSH", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		_ = file.Close()
		return DownloadResult{}, downloadOperationError(server.ID, "create SSH session", err)
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = file.Close()
		return DownloadResult{}, downloadOperationError(server.ID, "prepare remote stream", err)
	}
	stderr := &boundedDownloadBuffer{}
	session.SetStderr(stderr)
	if err := session.Start(command); err != nil {
		_ = file.Close()
		return DownloadResult{}, downloadOperationError(server.ID, "start remote stream", err)
	}

	var copyResult DownloadResult
	err = runSSHDownloadWithContext(ctx, func() {
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		_ = client.Close()
	}, session.Wait, func() error {
		var copyErr error
		copyResult, copyErr = copyDownload(ctx, file, stdout)
		return copyErr
	})
	closeErr := file.Close()
	if err != nil {
		return DownloadResult{}, downloadOperationError(server.ID, "transfer remote archive", err)
	}
	if closeErr != nil {
		return DownloadResult{}, downloadOperationError(server.ID, "close local partial", closeErr)
	}
	created = false
	return copyResult, nil
}

func copyDownload(ctx context.Context, dst io.Writer, src io.Reader) (DownloadResult, error) {
	hash := sha256.New()
	buffer := make([]byte, 32*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return DownloadResult{}, err
		}
		read, readErr := src.Read(buffer)
		if read > 0 {
			written, writeErr := dst.Write(buffer[:read])
			if written > 0 {
				_, _ = hash.Write(buffer[:written])
				size += int64(written)
			}
			if writeErr != nil {
				return DownloadResult{}, writeErr
			}
			if written != read {
				return DownloadResult{}, io.ErrShortWrite
			}
			if err := ctx.Err(); err != nil {
				return DownloadResult{}, err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return DownloadResult{Size: size, SHA256: fmt.Sprintf("%x", hash.Sum(nil))}, nil
			}
			return DownloadResult{}, readErr
		}
	}
}

func downloadCommand(remotePath string) (string, error) {
	if remotePath == "" || !path.IsAbs(remotePath) || path.Clean(remotePath) != remotePath || strings.ContainsAny(remotePath, "\x00\r\n\\") {
		return "", errors.New("remote archive path is not canonical")
	}
	components := strings.Split(remotePath, "/")
	if len(components) != 7 || components[0] != "" || strings.Join(components[1:5], "/") != strings.TrimPrefix(mysqlBackupRemoteRoot, "/") {
		return "", errors.New("remote archive path is outside the controlled root")
	}
	if !mysqlBackupTaskIDPattern.MatchString(components[5]) || components[5] == "." || components[5] == ".." {
		return "", errors.New("remote archive task component is invalid")
	}
	if components[6] != mysqlBackupArchiveName {
		return "", errors.New("remote archive name is invalid")
	}
	return "python3 -c " + shellQuote(remoteDownloadHelper) + " " + shellQuote(remotePath), nil
}

func runSSHDownloadWithContext(ctx context.Context, cancelTransfer func(), waitRemote func() error, copyOutput func() error) error {
	if err := ctx.Err(); err != nil {
		if cancelTransfer != nil {
			cancelTransfer()
		}
		return err
	}
	waitErrCh := make(chan error, 1)
	copyErrCh := make(chan error, 1)
	go func() { waitErrCh <- waitRemote() }()
	go func() { copyErrCh <- copyOutput() }()

	var waitErr error
	var copyErr error
	waitDone := false
	copyDone := false
	for !waitDone || !copyDone {
		select {
		case err := <-waitErrCh:
			waitErr = err
			waitDone = true
		case err := <-copyErrCh:
			copyErr = err
			copyDone = true
		case <-ctx.Done():
			if cancelTransfer != nil {
				cancelTransfer()
			}
			drainSSHDownload(waitErrCh, copyErrCh, &waitErr, &copyErr, &waitDone, &copyDone)
			return ctx.Err()
		}
		if (waitDone && waitErr != nil && !copyDone) || (copyDone && copyErr != nil && !waitDone) {
			if cancelTransfer != nil {
				cancelTransfer()
			}
			drainSSHDownload(waitErrCh, copyErrCh, &waitErr, &copyErr, &waitDone, &copyDone)
			break
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if copyErr != nil {
		return copyErr
	}
	return waitErr
}

func drainSSHDownload(waitErrCh, copyErrCh <-chan error, waitErr, copyErr *error, waitDone, copyDone *bool) {
	timer := time.NewTimer(sshCancelDrainTimeout)
	defer timer.Stop()
	for !*waitDone || !*copyDone {
		select {
		case err := <-waitErrCh:
			*waitErr = err
			*waitDone = true
		case err := <-copyErrCh:
			*copyErr = err
			*copyDone = true
		case <-timer.C:
			return
		}
	}
}

func removeCreatedDownload(localPath string, created os.FileInfo) error {
	current, err := os.Lstat(localPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if created != nil && (current.Mode()&os.ModeSymlink != 0 || !os.SameFile(created, current)) {
		return errors.New("local partial identity changed")
	}
	return os.Remove(localPath)
}

func downloadOperationError(serverID, operation string, cause error) error {
	id := strings.TrimSpace(serverID)
	if !mysqlBackupTaskIDPattern.MatchString(id) {
		id = "unknown"
	}
	if errors.Is(cause, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("download file from server %s: %s failed", id, operation)
}
