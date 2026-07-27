package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"

	"golang.org/x/crypto/ssh"
)

func TestCopyDownloadReportsSizeAndSHA256(t *testing.T) {
	var dst bytes.Buffer

	result, err := copyDownload(context.Background(), &dst, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("copyDownload returned error: %v", err)
	}
	if result.Size != 5 {
		t.Fatalf("size = %d, want 5", result.Size)
	}
	const wantSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if result.SHA256 != wantSHA256 {
		t.Fatalf("SHA256 = %q, want %q", result.SHA256, wantSHA256)
	}
	if dst.String() != "hello" {
		t.Fatalf("destination = %q, want hello", dst.String())
	}
}

func TestCopyDownloadRejectsShortWrite(t *testing.T) {
	_, err := copyDownload(context.Background(), shortDownloadWriter{}, strings.NewReader("archive"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want io.ErrShortWrite", err)
	}
}

func TestCopyDownloadReturnsSourceError(t *testing.T) {
	wantErr := errors.New("source failed")
	var dst bytes.Buffer

	_, err := copyDownload(context.Background(), &dst, &downloadErrorReader{data: []byte("part"), err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want source error", err)
	}
	if dst.String() != "part" {
		t.Fatalf("destination = %q, want source bytes written before the error", dst.String())
	}
}

func TestCopyDownloadReturnsDestinationError(t *testing.T) {
	wantErr := errors.New("destination failed")

	_, err := copyDownload(context.Background(), downloadErrorWriter{err: wantErr}, strings.NewReader("archive"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want destination error", err)
	}
}

func TestCopyDownloadStopsWhenContextIsCancelledDuringStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &cancelAfterDownloadReader{cancel: cancel}
	var dst bytes.Buffer

	_, err := copyDownload(ctx, &dst, src)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if src.reads != 1 {
		t.Fatalf("source reads = %d, want 1", src.reads)
	}
	if dst.String() != "first" {
		t.Fatalf("destination = %q, want first", dst.String())
	}
}

func TestDownloadCommandAcceptsControlledMySQLArchive(t *testing.T) {
	const remotePath = "/aifar/apps/mysql/_backup/task-123/dump.tar"

	command, err := downloadCommand(remotePath)
	if err != nil {
		t.Fatalf("downloadCommand returned error: %v", err)
	}
	if !strings.HasPrefix(command, "python3 -c ") {
		t.Fatalf("command does not invoke the fixed descriptor helper: %q", command)
	}
	if strings.Count(command, remotePath) != 1 {
		t.Fatalf("remote path occurs %d times, want exactly one argv occurrence in %q", strings.Count(command, remotePath), command)
	}
	if strings.Contains(command, "test ! -L") || strings.Contains(command, "cat --") {
		t.Fatalf("command retains check-then-reopen semantics: %q", command)
	}
}

func TestDownloadCommandRejectsUncontrolledPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "relative", path: "aifar/apps/mysql/_backup/task-123/dump.tar"},
		{name: "wrong root", path: "/tmp/task-123/dump.tar"},
		{name: "missing task", path: "/aifar/apps/mysql/_backup/dump.tar"},
		{name: "nested archive", path: "/aifar/apps/mysql/_backup/task-123/nested/dump.tar"},
		{name: "newline", path: "/aifar/apps/mysql/_backup/task-123/dump.tar\nwhoami"},
		{name: "carriage return", path: "/aifar/apps/mysql/_backup/task-123/dump.tar\rwhoami"},
		{name: "nul", path: "/aifar/apps/mysql/_backup/task-123/dump.tar\x00whoami"},
		{name: "task traversal", path: "/aifar/apps/mysql/_backup/../task-123/dump.tar"},
		{name: "archive traversal", path: "/aifar/apps/mysql/_backup/task-123/../dump.tar"},
		{name: "task shell fragment", path: "/aifar/apps/mysql/_backup/task-123;whoami/dump.tar"},
		{name: "archive shell fragment", path: "/aifar/apps/mysql/_backup/task-123/dump.tar;whoami"},
		{name: "unexpected archive", path: "/aifar/apps/mysql/_backup/task-123/other.tar"},
		{name: "backslash", path: "/aifar/apps/mysql/_backup/task-123\\dump.tar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, err := downloadCommand(tt.path)
			if err == nil {
				t.Fatalf("downloadCommand(%q) succeeded with %q", tt.path, command)
			}
			if command != "" {
				t.Fatalf("rejected path produced command %q", command)
			}
		})
	}
}

func TestDownloadFileStreamsOneSessionIntoExclusivePartial(t *testing.T) {
	payload := []byte("controlled archive")
	session := &fakeDownloadSession{stdout: bytes.NewReader(payload)}
	client := &fakeDownloadClient{session: session}
	localPath := filepath.Join(t.TempDir(), "dump.tar.partial")
	server := store.Server{ID: "server-1"}

	result, err := downloadSSHFileWithDialer(context.Background(), server, "/aifar/apps/mysql/_backup/task-123/dump.tar", localPath, 0o600, func(context.Context, store.Server) (sshDownloadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("download returned error: %v", err)
	}
	if client.newSessionCalls != 1 {
		t.Fatalf("NewSession calls = %d, want 1", client.newSessionCalls)
	}
	if session.startedCommand == "" {
		t.Fatal("remote command was not started")
	}
	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read partial: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("partial bytes = %q, want %q", got, payload)
	}
	if result.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", result.Size, len(payload))
	}
	wantSum := fmt.Sprintf("%x", sha256.Sum256(payload))
	if result.SHA256 != wantSum {
		t.Fatalf("SHA256 = %q, want %q", result.SHA256, wantSum)
	}
	info, err := os.Stat(localPath)
	if err != nil {
		t.Fatalf("stat partial: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("partial mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestDownloadFileDoesNotOverwriteOrRemoveExistingPartial(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "dump.tar.partial")
	if err := os.WriteFile(localPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("seed existing partial: %v", err)
	}
	dialCalls := 0

	_, err := downloadSSHFileWithDialer(context.Background(), store.Server{ID: "server-1"}, "/aifar/apps/mysql/_backup/task-123/dump.tar", localPath, 0o600, func(context.Context, store.Server) (sshDownloadClient, error) {
		dialCalls++
		return nil, errors.New("must not dial")
	})
	if err == nil {
		t.Fatal("download unexpectedly succeeded")
	}
	if dialCalls != 0 {
		t.Fatalf("dial calls = %d, want 0", dialCalls)
	}
	got, readErr := os.ReadFile(localPath)
	if readErr != nil {
		t.Fatalf("read existing partial: %v", readErr)
	}
	if string(got) != "existing" {
		t.Fatalf("existing partial = %q, want unchanged", got)
	}
}

func TestDownloadFileRemovesCreatedPartialAndSanitizesRemoteFailure(t *testing.T) {
	secret := strings.Repeat("private-output-from-remote", 1000)
	commandText := "arbitrary command text"
	session := &fakeDownloadSession{
		stdout:  strings.NewReader("partial archive"),
		waitErr: errors.New(commandText),
		stderr:  secret,
	}
	localPath := filepath.Join(t.TempDir(), "dump.tar.partial")

	_, err := downloadSSHFileWithDialer(context.Background(), store.Server{ID: "server-1"}, "/aifar/apps/mysql/_backup/task-123/dump.tar", localPath, 0o600, func(context.Context, store.Server) (sshDownloadClient, error) {
		return &fakeDownloadClient{session: session}, nil
	})
	if err == nil {
		t.Fatal("download unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "server-1") || !strings.Contains(err.Error(), "transfer") {
		t.Fatalf("error lacks sanitized operation context: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), commandText) || strings.Contains(err.Error(), "task-123") {
		t.Fatalf("error leaked remote details: %v", err)
	}
	if _, statErr := os.Stat(localPath); !os.IsNotExist(statErr) {
		t.Fatalf("created partial remains after failure: %v", statErr)
	}
	if session.stderrWriter == nil {
		t.Fatal("stderr was not captured")
	}
	if got := session.stderrWriter.String(); len(got) > maxDownloadStderrBytes {
		t.Fatalf("captured stderr length = %d, want at most %d", len(got), maxDownloadStderrBytes)
	}
}

func TestDownloadFileCancellationClosesSessionAndRemovesCreatedPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	started := make(chan struct{})
	closed := make(chan struct{})
	var closeOnce sync.Once
	session := &fakeDownloadSession{
		stdout: reader,
		start: func() {
			close(started)
		},
		wait: func() error {
			<-closed
			return errors.New("session closed")
		},
		close: func() {
			closeOnce.Do(func() {
				_ = writer.CloseWithError(errors.New("session closed"))
				close(closed)
			})
		},
	}
	localPath := filepath.Join(t.TempDir(), "dump.tar.partial")
	resultCh := make(chan error, 1)

	go func() {
		_, err := downloadSSHFileWithDialer(ctx, store.Server{ID: "server-1"}, "/aifar/apps/mysql/_backup/task-123/dump.tar", localPath, 0o600, func(context.Context, store.Server) (sshDownloadClient, error) {
			return &fakeDownloadClient{session: session}, nil
		})
		resultCh <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("download session did not start")
	}
	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("download did not stop after cancellation")
	}
	if _, statErr := os.Stat(localPath); !os.IsNotExist(statErr) {
		t.Fatalf("created partial remains after cancellation: %v", statErr)
	}
}

func TestDownloadOperationErrorReturnsCanonicalContextErrorsWithoutWrappedCauseText(t *testing.T) {
	tests := []struct {
		name string
		want error
	}{
		{name: "cancelled", want: context.Canceled},
		{name: "deadline", want: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const secret = "wrapped-secret-value"
			cause := fmt.Errorf("%s: %w", secret, tt.want)

			got := downloadOperationError("server-1", "transfer remote archive", cause)
			if got != tt.want {
				t.Fatalf("error = %v, want canonical %v", got, tt.want)
			}
			if !errors.Is(got, tt.want) {
				t.Fatalf("errors.Is(%v, %v) = false", got, tt.want)
			}
			if strings.Contains(got.Error(), secret) {
				t.Fatalf("canonical context error leaked wrapped cause text: %v", got)
			}
		})
	}
}

func TestDownloadFilePreservesReplacementWhenCreatedPartialPathChangesBeforeCleanup(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "dump.tar.partial")
	movedPath := localPath + ".owned"
	eof := make(chan struct{})
	source := &notifyDownloadEOFReader{reader: strings.NewReader("owned partial"), eof: eof}
	const replacement = "replacement-must-survive"
	var replacementErr error
	replaced := false
	session := &fakeDownloadSession{
		stdout: source,
		wait: func() error {
			<-eof
			return errors.New("remote-secret-failure")
		},
		close: func() {
			if replaced {
				return
			}
			if err := os.Rename(localPath, movedPath); err != nil {
				replacementErr = err
				return
			}
			replacementErr = os.WriteFile(localPath, []byte(replacement), 0o600)
			replaced = replacementErr == nil
		},
	}

	_, err := downloadSSHFileWithDialer(context.Background(), store.Server{ID: "server-1"}, "/aifar/apps/mysql/_backup/task-123/dump.tar", localPath, 0o600, func(context.Context, store.Server) (sshDownloadClient, error) {
		return &fakeDownloadClient{session: session}, nil
	})
	if replacementErr != nil {
		t.Fatalf("replace created partial during fake transfer close: %v", replacementErr)
	}
	if err == nil {
		t.Fatal("download unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "server-1") || !strings.Contains(err.Error(), "transfer") {
		t.Fatalf("error lacks sanitized operation context: %v", err)
	}
	for _, forbidden := range []string{"remote-secret-failure", replacement, localPath, "task-123"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error leaked %q: %v", forbidden, err)
		}
	}
	got, readErr := os.ReadFile(localPath)
	if readErr != nil {
		t.Fatalf("read replacement partial: %v", readErr)
	}
	if string(got) != replacement {
		t.Fatalf("replacement = %q, want %q", got, replacement)
	}
	owned, readErr := os.ReadFile(movedPath)
	if readErr != nil {
		t.Fatalf("read renamed created partial: %v", readErr)
	}
	if string(owned) != "owned partial" {
		t.Fatalf("renamed created partial = %q, want owned partial", owned)
	}
}

type shortDownloadWriter struct{}

func (shortDownloadWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
}

type downloadErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *downloadErrorReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, r.data), r.err
}

type downloadErrorWriter struct {
	err error
}

func (w downloadErrorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type cancelAfterDownloadReader struct {
	cancel context.CancelFunc
	reads  int
}

type notifyDownloadEOFReader struct {
	reader io.Reader
	eof    chan struct{}
	once   sync.Once
}

func (r *notifyDownloadEOFReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.once.Do(func() { close(r.eof) })
	}
	return n, err
}

func (r *cancelAfterDownloadReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, errors.New("read after cancellation")
	}
	n := copy(p, "first")
	r.cancel()
	return n, nil
}

type fakeDownloadClient struct {
	session         *fakeDownloadSession
	newSessionCalls int
}

func (c *fakeDownloadClient) NewSession() (sshDownloadSession, error) {
	c.newSessionCalls++
	return c.session, nil
}

func (c *fakeDownloadClient) Close() error { return nil }

type fakeDownloadSession struct {
	stdout         io.Reader
	stderr         string
	stderrWriter   *boundedDownloadBuffer
	waitErr        error
	wait           func() error
	start          func()
	close          func()
	startedCommand string
}

func (s *fakeDownloadSession) StdoutPipe() (io.Reader, error) {
	return s.stdout, nil
}

func (s *fakeDownloadSession) SetStderr(dst *boundedDownloadBuffer) {
	s.stderrWriter = dst
	_, _ = io.WriteString(dst, s.stderr)
}

func (s *fakeDownloadSession) Start(command string) error {
	s.startedCommand = command
	if s.start != nil {
		s.start()
	}
	return nil
}

func (s *fakeDownloadSession) Wait() error {
	if s.wait != nil {
		return s.wait()
	}
	return s.waitErr
}

func (s *fakeDownloadSession) Signal(ssh.Signal) error { return nil }

func (s *fakeDownloadSession) Close() error {
	if s.close != nil {
		s.close()
	}
	return nil
}
