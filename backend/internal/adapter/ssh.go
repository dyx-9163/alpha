package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
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

type SSHRemote struct{}

func (SSHRemote) Run(ctx context.Context, server store.Server, command string) (CommandResult, error) {
	return RunSSH(ctx, server, command)
}

func (SSHRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	return UploadSSHFile(ctx, server, localPath, remotePath, mode)
}

func (SSHRemote) StreamFile(ctx context.Context, server store.Server, remotePath string, dst io.Writer) (int64, error) {
	return StreamSSHFile(ctx, server, remotePath, dst)
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
	stopWatchingContext := make(chan struct{})
	contextWatcherDone := make(chan struct{})
	go func() {
		defer close(contextWatcherDone)
		select {
		case <-ctx.Done():
			cancel()
		case <-stopWatchingContext:
		}
	}()

	writer := &sshStreamWriter{dst: dst}
	copied, copyErr := io.Copy(writer, stdout)
	if writer.err != nil {
		cancel()
	}
	close(stopWatchingContext)
	<-contextWatcherDone
	waitErr := <-waitErrCh

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
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
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
