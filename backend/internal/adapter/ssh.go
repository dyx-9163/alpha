package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aifar-deployment/backend/internal/logmask"
	"aifar-deployment/backend/internal/store"

	"golang.org/x/crypto/ssh"
)

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type CommandStreamResult struct {
	Bytes  int64
	Stderr string
}

type SSHRemote struct{}

func (SSHRemote) Run(ctx context.Context, server store.Server, command string) (CommandResult, error) {
	return RunSSH(ctx, server, command)
}

func (SSHRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	return UploadSSHFile(ctx, server, localPath, remotePath, mode)
}

func (SSHRemote) UploadFileAtomicVerified(ctx context.Context, server store.Server, localPath, remoteDir, finalName string, mode os.FileMode, expectedSize int64, expectedSHA256 string) (string, error) {
	return UploadSSHFileAtomicVerified(ctx, server, localPath, remoteDir, finalName, mode, expectedSize, expectedSHA256)
}

func (SSHRemote) StreamFile(ctx context.Context, server store.Server, remotePath string, dst io.Writer) (int64, error) {
	return StreamSSHFile(ctx, server, remotePath, dst)
}

func (SSHRemote) StreamCommand(ctx context.Context, server store.Server, command string, dst io.Writer) (CommandStreamResult, error) {
	return StreamSSHCommand(ctx, server, command, dst)
}

func (SSHRemote) DownloadFile(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode) (DownloadResult, error) {
	return DownloadSSHFile(ctx, server, remotePath, localPath, mode)
}

func ProbeSSH(ctx context.Context, server store.Server) error {
	client, err := dialSSH(ctx, server)
	if err != nil {
		return err
	}
	defer client.Close()
	return nil
}

func DialSSH(ctx context.Context, server store.Server) (*ssh.Client, error) {
	return dialSSH(ctx, server)
}

func RunSSH(ctx context.Context, server store.Server, command string) (CommandResult, error) {
	client, err := dialSSH(ctx, server)
	if err != nil {
		return CommandResult{}, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return CommandResult{}, err
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	err = runSSHCommandWithContext(ctx, func() {
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		_ = client.Close()
	}, func() error {
		return session.Run(command)
	})
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		return result, err
	}
	return result, nil
}

const sshCancelDrainTimeout = 50 * time.Millisecond

const sshStreamStderrLimit = 8 * 1024

type boundedSSHStderr struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	remaining int
}

func newBoundedSSHStderr(limit int) *boundedSSHStderr {
	return &boundedSSHStderr{remaining: limit}
}

func (b *boundedSSHStderr) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(value)
	if b.remaining == 0 {
		return written, nil
	}
	if len(value) > b.remaining {
		value = value[:b.remaining]
	}
	_, _ = b.buf.Write(value)
	b.remaining -= len(value)
	return written, nil
}

func (b *boundedSSHStderr) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func runSSHCommandWithContext(ctx context.Context, cancelCommand func(), run func() error) error {
	if err := ctx.Err(); err != nil {
		if cancelCommand != nil {
			cancelCommand()
		}
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- run()
	}()
	select {
	case err := <-errCh:
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	case <-ctx.Done():
		if cancelCommand != nil {
			cancelCommand()
		}
		select {
		case <-errCh:
		case <-time.After(sshCancelDrainTimeout):
		}
		return ctx.Err()
	}
}

func runSSHUploadWithContext(ctx context.Context, cancelCommand func(), run func() error, copyInput func() error, stderr func() string) error {
	if err := ctx.Err(); err != nil {
		if cancelCommand != nil {
			cancelCommand()
		}
		return err
	}
	runErrCh := make(chan error, 1)
	copyErrCh := make(chan error, 1)
	go func() {
		runErrCh <- run()
	}()
	go func() {
		copyErrCh <- copyInput()
	}()

	var runErr error
	var copyErr error
	runDone := false
	copyDone := false
	for !runDone || !copyDone {
		select {
		case err := <-runErrCh:
			runErr = err
			runDone = true
		case err := <-copyErrCh:
			copyErr = err
			copyDone = true
		case <-ctx.Done():
			if cancelCommand != nil {
				cancelCommand()
			}
			drainSSHUpload(runErrCh, copyErrCh, &runErr, &copyErr, &runDone, &copyDone)
			return ctx.Err()
		}
		if (runDone && runErr != nil && !copyDone) || (copyDone && copyErr != nil && !runDone) {
			if cancelCommand != nil {
				cancelCommand()
			}
			drainSSHUpload(runErrCh, copyErrCh, &runErr, &copyErr, &runDone, &copyDone)
			break
		}
	}
	if copyErr != nil {
		return uploadError(copyErr, stderr())
	}
	return uploadError(runErr, stderr())
}

func drainSSHUpload(runErrCh, copyErrCh <-chan error, runErr, copyErr *error, runDone, copyDone *bool) {
	timer := time.NewTimer(sshCancelDrainTimeout)
	defer timer.Stop()
	for !*runDone || !*copyDone {
		select {
		case err := <-runErrCh:
			*runErr = err
			*runDone = true
		case err := <-copyErrCh:
			*copyErr = err
			*copyDone = true
		case <-timer.C:
			return
		}
	}
}

func StreamSSHLines(ctx context.Context, server store.Server, command string, onLine func(string)) error {
	client, err := dialSSH(ctx, server)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return err
	}
	if err := session.Start(command); err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanDockerLogLines(stdout, onLine)
	}()
	go func() {
		defer wg.Done()
		scanDockerLogLines(stderr, onLine)
	}()
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Wait()
	}()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		_ = client.Close()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
		wg.Wait()
		return nil
	case err := <-errCh:
		wg.Wait()
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func StreamSSHFile(ctx context.Context, server store.Server, remotePath string, dst io.Writer) (int64, error) {
	if strings.TrimSpace(remotePath) == "" {
		return 0, fmt.Errorf("remote file path is required")
	}
	if dst == nil {
		return 0, fmt.Errorf("destination writer is required")
	}
	client, err := dialSSH(ctx, server)
	if err != nil {
		return 0, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return 0, err
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return 0, err
	}
	stderr := newBoundedSSHStderr(sshStreamStderrLimit)
	session.Stderr = stderr
	if err := session.Start("cat " + shellQuote(remotePath)); err != nil {
		return 0, streamSSHError(err, stderr.String())
	}
	return streamSSHOutputWithContext(ctx, dst, stdout, session.Wait, func() {
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		_ = client.Close()
	}, stderr.String)
}

func StreamSSHCommand(ctx context.Context, server store.Server, command string, dst io.Writer) (CommandStreamResult, error) {
	if err := validateSSHCommandStreamDestination(command, dst); err != nil {
		return CommandStreamResult{}, err
	}
	client, err := dialSSH(ctx, server)
	if err != nil {
		return CommandStreamResult{}, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return CommandStreamResult{}, err
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return CommandStreamResult{}, err
	}
	stderr := newBoundedSSHStderr(sshStreamStderrLimit)
	session.Stderr = stderr
	if err := session.Start(command); err != nil {
		return commandStreamResult(0, stderr), streamSSHError(err, stderr.String())
	}
	return streamSSHCommandOutputWithContext(ctx, command, dst, stdout, session.Wait, func() {
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		_ = client.Close()
	}, stderr)
}

func streamSSHCommandOutputWithContext(
	ctx context.Context,
	command string,
	dst io.Writer,
	stdout io.Reader,
	wait func() error,
	cancelCommand func(),
	stderr *boundedSSHStderr,
) (CommandStreamResult, error) {
	if err := validateSSHCommandStreamInput(command, dst, stdout, wait); err != nil {
		return CommandStreamResult{}, err
	}
	if stderr == nil {
		stderr = newBoundedSSHStderr(sshStreamStderrLimit)
	}
	copied, err := streamSSHOutputWithContext(ctx, dst, stdout, wait, cancelCommand, stderr.String)
	return commandStreamResult(copied, stderr), err
}

func validateSSHCommandStreamInput(command string, dst io.Writer, stdout io.Reader, wait func() error) error {
	if err := validateSSHCommandStreamDestination(command, dst); err != nil {
		return err
	}
	if stdout == nil {
		return fmt.Errorf("SSH stdout reader is required")
	}
	if wait == nil {
		return fmt.Errorf("SSH wait function is required")
	}
	return nil
}

func validateSSHCommandStreamDestination(command string, dst io.Writer) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("SSH command is required")
	}
	if dst == nil {
		return fmt.Errorf("destination writer is required")
	}
	return nil
}

func commandStreamResult(copied int64, stderr *boundedSSHStderr) CommandStreamResult {
	stderrText := ""
	if stderr != nil {
		stderrText = strings.TrimSpace(logmask.Mask(stderr.String()))
	}
	return CommandStreamResult{Bytes: copied, Stderr: stderrText}
}

type sshStreamWriter struct {
	dst io.Writer
	err error
}

func (w *sshStreamWriter) Write(value []byte) (int, error) {
	written, err := w.dst.Write(value)
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err != nil && w.err == nil {
		w.err = err
	}
	return written, err
}

func streamSSHOutputWithContext(ctx context.Context, dst io.Writer, stdout io.Reader, wait func() error, cancelCommand func(), stderr func() string) (int64, error) {
	var cancelOnce sync.Once
	cancel := func() {
		if cancelCommand != nil {
			cancelOnce.Do(cancelCommand)
		}
	}
	if err := ctx.Err(); err != nil {
		cancel()
		return 0, err
	}

	waitErrCh := make(chan error, 1)
	go func() {
		waitErrCh <- wait()
	}()
	operationDone := make(chan struct{})
	contextWatcherDone := make(chan struct{})
	go func() {
		defer close(contextWatcherDone)
		select {
		case <-ctx.Done():
			cancel()
		case <-operationDone:
		}
	}()

	writer := &sshStreamWriter{dst: dst}
	copied, copyErr := io.Copy(writer, stdout)
	if writer.err != nil {
		cancel()
	}
	waitErr := <-waitErrCh
	if ctx.Err() != nil {
		cancel()
	}
	close(operationDone)
	<-contextWatcherDone

	if writer.err != nil {
		return copied, streamSSHError(writer.err, stderr())
	}
	if err := ctx.Err(); err != nil {
		return copied, err
	}
	if waitErr != nil {
		return copied, streamSSHError(waitErr, stderr())
	}
	return copied, streamSSHError(copyErr, stderr())
}

func UploadSSHFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	client, err := dialSSH(ctx, server)
	if err != nil {
		return err
	}
	defer client.Close()
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	tmpPath := remotePath + ".uploading"
	var stderr bytes.Buffer
	session.Stderr = &stderr
	command := fmt.Sprintf("mkdir -p %s && cat > %s && chmod %04o %s && mv -f %s %s",
		shellQuote(filepath.ToSlash(filepath.Dir(remotePath))),
		shellQuote(tmpPath),
		mode.Perm(),
		shellQuote(tmpPath),
		shellQuote(tmpPath),
		shellQuote(remotePath),
	)
	return runSSHUploadWithContext(ctx, func() {
		_ = stdin.Close()
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		_ = client.Close()
	}, func() error {
		return session.Run(command)
	}, func() error {
		_, copyErr := io.Copy(stdin, file)
		closeErr := stdin.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}, func() string {
		return stderr.String()
	})
}

// UploadSSHFileAtomicVerified streams a file into a fresh, mode-0700 staging
// directory. The remote shell opens the partial leaf with noclobber semantics,
// verifies the exact byte count and SHA-256, then atomically renames it. The
// random staging path is generated locally from cryptographic entropy and is
// never derived from uploaded content.
func UploadSSHFileAtomicVerified(ctx context.Context, server store.Server, localPath, remoteDir, finalName string, mode os.FileMode, expectedSize int64, expectedSHA256 string) (string, error) {
	remoteDir = filepath.ToSlash(filepath.Clean(strings.TrimSpace(remoteDir)))
	finalName = strings.TrimSpace(finalName)
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if !strings.HasPrefix(remoteDir, "/") || remoteDir == "/" || strings.ContainsAny(remoteDir, "\x00\r\n") {
		return "", errors.New("atomic upload remote directory is invalid")
	}
	if finalName == "" || finalName == "." || finalName == ".." || strings.ContainsAny(finalName, "/\\\x00\r\n") {
		return "", errors.New("atomic upload final name is invalid")
	}
	decodedHash, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(decodedHash) != 32 || expectedSize < 0 || mode.Perm() != mode || mode.Perm() == 0 {
		return "", errors.New("atomic upload verification input is invalid")
	}
	tokenBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return "", errors.New("atomic upload staging identity generation failed")
	}
	stageDir := path.Join(remoteDir, ".aifar-stage-"+hex.EncodeToString(tokenBytes))
	partialPath := path.Join(stageDir, ".payload.part")
	finalPath := path.Join(stageDir, finalName)
	command := atomicVerifiedUploadCommand(remoteDir, stageDir, partialPath, finalPath, mode, expectedSize, expectedSHA256)

	client, err := dialSSH(ctx, server)
	if err != nil {
		return "", err
	}
	file, err := os.Open(localPath)
	if err != nil {
		_ = client.Close()
		return "", err
	}
	session, err := client.NewSession()
	if err != nil {
		_ = file.Close()
		_ = client.Close()
		return "", err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = file.Close()
		_ = client.Close()
		return "", err
	}
	var stderr bytes.Buffer
	session.Stderr = &stderr
	uploadErr := runSSHUploadWithContext(ctx, func() {
		_ = stdin.Close()
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		_ = client.Close()
	}, func() error {
		return session.Run(command)
	}, func() error {
		_, copyErr := io.Copy(stdin, file)
		closeErr := stdin.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}, func() string {
		return logmask.Mask(stderr.String())
	})
	_ = file.Close()
	_ = session.Close()
	_ = client.Close()
	if uploadErr != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cleanupCommand := "rm -f -- " + shellQuote(partialPath) + " " + shellQuote(finalPath) + " && { rmdir -- " + shellQuote(stageDir) + " 2>/dev/null || [ ! -e " + shellQuote(stageDir) + " ]; }"
		if _, cleanupErr := RunSSH(cleanupCtx, server, cleanupCommand); cleanupErr != nil {
			return "", errors.New("atomic upload cleanup failed")
		}
		return "", errors.New("atomic verified upload failed")
	}
	return finalPath, nil
}

func atomicVerifiedUploadCommand(remoteDir, stageDir, partialPath, finalPath string, mode os.FileMode, expectedSize int64, expectedSHA256 string) string {
	return strings.Join([]string{
		"set -eu",
		"set -f",
		"umask 077",
		"base=" + shellQuote(remoteDir),
		"stage=" + shellQuote(stageDir),
		"part=" + shellQuote(partialPath),
		"final=" + shellQuote(finalPath),
		"current=",
		"old_ifs=$IFS",
		"IFS=/",
		"for component in ${base#/}; do [ -n \"$component\" ] || continue; current=\"$current/$component\"; if [ -L \"$current\" ]; then exit 71; elif [ -e \"$current\" ]; then [ -d \"$current\" ] || exit 72; else mkdir -m 0700 -- \"$current\"; fi; done",
		"IFS=$old_ifs",
		"[ ! -L \"$base\" ] && [ -d \"$base\" ]",
		"mkdir -m 0700 -- \"$stage\"",
		"cleanup() { rm -f -- \"$part\" \"$final\"; rmdir -- \"$stage\"; }",
		"trap 'code=$?; if [ \"$code\" -ne 0 ]; then cleanup || exit 79; fi; exit \"$code\"' EXIT HUP INT TERM",
		"( set -C; exec 3> \"$part\"; cat >&3 )",
		"chmod " + fmt.Sprintf("%04o", mode.Perm()) + " -- \"$part\"",
		"[ \"$(wc -c < \"$part\" | tr -d '[:space:]')\" = " + shellQuote(fmt.Sprintf("%d", expectedSize)) + " ]",
		"[ \"$(sha256sum -- \"$part\" | awk '{print $1}')\" = " + shellQuote(expectedSHA256) + " ]",
		"mv -- \"$part\" \"$final\"",
		"[ ! -L \"$final\" ] && [ -f \"$final\" ]",
		"[ \"$(wc -c < \"$final\" | tr -d '[:space:]')\" = " + shellQuote(fmt.Sprintf("%d", expectedSize)) + " ]",
		"[ \"$(sha256sum -- \"$final\" | awk '{print $1}')\" = " + shellQuote(expectedSHA256) + " ]",
		"sync -f \"$final\"",
		"sync -d \"$stage\"",
		"trap - EXIT HUP INT TERM",
	}, "\n")
}

func dialSSH(ctx context.Context, server store.Server) (*ssh.Client, error) {
	authMethods := []ssh.AuthMethod{}
	if server.Password != "" {
		authMethods = append(authMethods, ssh.Password(server.Password))
	}
	if server.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(server.PrivateKey))
		if err != nil {
			return nil, err
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("server has no usable SSH credential")
	}
	cfg := &ssh.ClientConfig{
		User:            server.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(server.Host, fmt.Sprintf("%d", server.Port))
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	stopped := stopCancel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !stopped && ctx.Err() != nil {
		_ = c.Close()
		_ = conn.Close()
		return nil, ctx.Err()
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = c.Close()
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func uploadError(err error, stderr string) error {
	if err == nil {
		return nil
	}
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

func streamSSHError(err error, stderr string) error {
	if err == nil {
		return nil
	}
	stderr = strings.TrimSpace(logmask.Mask(stderr))
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}
