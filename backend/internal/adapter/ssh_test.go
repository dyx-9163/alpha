package adapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestAtomicVerifiedUploadCommandRejectsSymlinkAndCleansHashFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("remote upload shell contract runs on Linux targets")
	}
	body := []byte("validated migration snapshot\n")
	digest := sha256.Sum256(body)
	hash := hex.EncodeToString(digest[:])

	t.Run("symlink parent cannot overwrite victim", func(t *testing.T) {
		root := t.TempDir()
		victimDir := filepath.Join(root, "victim")
		if err := os.Mkdir(victimDir, 0o700); err != nil {
			t.Fatal(err)
		}
		victimPath := filepath.Join(victimDir, "victim.txt")
		if err := os.WriteFile(victimPath, []byte("unchanged"), 0o600); err != nil {
			t.Fatal(err)
		}
		linkPath := filepath.Join(root, "linked")
		if err := os.Symlink(victimDir, linkPath); err != nil {
			t.Fatal(err)
		}
		remoteDir := filepath.ToSlash(filepath.Join(linkPath, "migration"))
		stageDir := remoteDir + "/.aifar-stage-test"
		command := atomicVerifiedUploadCommand(remoteDir, stageDir, stageDir+"/.payload.part", stageDir+"/runtime-spec.json", 0o600, int64(len(body)), hash)
		cmd := exec.Command("sh", "-c", command)
		cmd.Stdin = bytes.NewReader(body)
		if err := cmd.Run(); err == nil {
			t.Fatal("symlink parent was accepted")
		}
		got, err := os.ReadFile(victimPath)
		if err != nil || string(got) != "unchanged" {
			t.Fatalf("symlink victim changed: body=%q err=%v", got, err)
		}
	})

	t.Run("hash mismatch removes partial and final", func(t *testing.T) {
		remoteDir := filepath.ToSlash(filepath.Join(t.TempDir(), "migration"))
		stageDir := remoteDir + "/.aifar-stage-test"
		command := atomicVerifiedUploadCommand(remoteDir, stageDir, stageDir+"/.payload.part", stageDir+"/runtime-spec.json", 0o600, int64(len(body)), strings.Repeat("0", 64))
		cmd := exec.Command("sh", "-c", command)
		cmd.Stdin = bytes.NewReader(body)
		if err := cmd.Run(); err == nil {
			t.Fatal("hash mismatch was accepted")
		}
		if _, err := os.Lstat(stageDir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed verified upload left staging debris: %v", err)
		}
	})

	t.Run("exact bytes land atomically with requested mode", func(t *testing.T) {
		remoteDir := filepath.ToSlash(filepath.Join(t.TempDir(), "migration"))
		stageDir := remoteDir + "/.aifar-stage-test"
		finalPath := stageDir + "/runtime-spec.json"
		command := atomicVerifiedUploadCommand(remoteDir, stageDir, stageDir+"/.payload.part", finalPath, 0o600, int64(len(body)), hash)
		cmd := exec.Command("sh", "-c", command)
		cmd.Stdin = bytes.NewReader(body)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("verified upload command failed: %v output=%s", err, output)
		}
		got, err := os.ReadFile(finalPath)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("landed bytes differ: body=%q err=%v", got, err)
		}
		info, err := os.Stat(finalPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("landed mode=%v", info.Mode().Perm())
		}
		if _, err := os.Lstat(stageDir + "/.payload.part"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("successful upload left partial file: %v", err)
		}
	})
}

type closedPipeWriter struct{}

func (closedPipeWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

type blockingWriter struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (w blockingWriter) Write(value []byte) (int, error) {
	w.started <- struct{}{}
	<-w.release
	return len(value), nil
}

type cancelingWriter struct {
	cancel func()
}

type failAfterWriter struct {
	remaining int
	err       error
}

func (w *failAfterWriter) Write(value []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, w.err
	}
	if len(value) > w.remaining {
		written := w.remaining
		w.remaining = 0
		return written, w.err
	}
	w.remaining -= len(value)
	return len(value), nil
}

func TestStreamSSHCommandOutputCopiesBinaryAndReturnsBoundedStderr(t *testing.T) {
	payload := make([]byte, 128<<10)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	stdoutReader, stdoutWriter := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		_, err := stdoutWriter.Write(payload)
		if closeErr := stdoutWriter.Close(); err == nil {
			err = closeErr
		}
		writeDone <- err
	}()
	stderr := newBoundedSSHStderr(sshStreamStderrLimit)
	if _, err := stderr.Write(bytes.Repeat([]byte("e"), sshStreamStderrLimit+1024)); err != nil {
		t.Fatal(err)
	}
	var dst bytes.Buffer
	result, err := streamSSHCommandOutputWithContext(context.Background(), "internal-export-command", &dst, stdoutReader,
		func() error { return nil }, func() {}, stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if result.Bytes != int64(len(payload)) || !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("binary command stream changed: bytes=%d payload=%d", result.Bytes, len(dst.Bytes()))
	}
	if len(result.Stderr) != sshStreamStderrLimit {
		t.Fatalf("bounded stderr length=%d want=%d", len(result.Stderr), sshStreamStderrLimit)
	}
}

func TestStreamSSHCommandOutputCancelsWaitAndWriter(t *testing.T) {
	ctx, cancelContext := context.WithCancel(context.Background())
	stdoutReader, stdoutWriter := io.Pipe()
	waitCancelled := make(chan struct{})
	var cancelCalls atomic.Int32
	resultCh := make(chan error, 1)
	go func() {
		_, err := streamSSHCommandOutputWithContext(ctx, "internal-export-command", io.Discard, stdoutReader,
			func() error {
				<-waitCancelled
				return errors.New("session closed")
			},
			func() {
				cancelCalls.Add(1)
				close(waitCancelled)
				_ = stdoutWriter.CloseWithError(context.Canceled)
			},
			newBoundedSSHStderr(sshStreamStderrLimit),
		)
		resultCh <- err
	}()
	cancelContext()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("command stream did not unblock after cancellation")
	}
	if got := cancelCalls.Load(); got != 1 {
		t.Fatalf("cancel callback calls=%d want=1", got)
	}
}

func TestStreamSSHCommandOutputPropagatesDestinationWriteFailure(t *testing.T) {
	destinationErr := errors.New("destination failed")
	cancelled := make(chan struct{})
	var cancelCalls atomic.Int32
	writer := &failAfterWriter{remaining: 17, err: destinationErr}
	stderr := newBoundedSSHStderr(sshStreamStderrLimit)
	_, _ = stderr.Write([]byte("password=do-not-expose"))
	result, err := streamSSHCommandOutputWithContext(context.Background(), "secret-internal-command", writer, bytes.NewReader(bytes.Repeat([]byte("x"), 128)),
		func() error {
			<-cancelled
			return errors.New("session closed")
		},
		func() {
			cancelCalls.Add(1)
			close(cancelled)
		},
		stderr,
	)
	if !errors.Is(err, destinationErr) {
		t.Fatalf("destination error=%v", err)
	}
	if result.Bytes != 17 {
		t.Fatalf("copied bytes=%d want=17", result.Bytes)
	}
	if got := cancelCalls.Load(); got != 1 {
		t.Fatalf("cancel callback calls=%d want=1", got)
	}
	if strings.Contains(err.Error(), "secret-internal-command") || strings.Contains(err.Error(), "do-not-expose") {
		t.Fatalf("stream error leaked command or stderr secret: %v", err)
	}
}

func TestStreamSSHCommandOutputRejectsEmptyCommandOrNilWriter(t *testing.T) {
	waitCalls := 0
	wait := func() error {
		waitCalls++
		return nil
	}
	if _, err := streamSSHCommandOutputWithContext(context.Background(), " ", io.Discard, bytes.NewReader(nil), wait, func() {}, newBoundedSSHStderr(sshStreamStderrLimit)); err == nil {
		t.Fatal("empty command was accepted")
	}
	if _, err := streamSSHCommandOutputWithContext(context.Background(), "internal-command", nil, bytes.NewReader(nil), wait, func() {}, newBoundedSSHStderr(sshStreamStderrLimit)); err == nil {
		t.Fatal("nil destination writer was accepted")
	}
	if waitCalls != 0 {
		t.Fatalf("validation invoked remote wait %d times", waitCalls)
	}
}

func TestStreamSSHCommandRejectsInvalidInputBeforeDial(t *testing.T) {
	server := store.Server{}
	if _, err := StreamSSHCommand(context.Background(), server, " ", io.Discard); err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("empty command error=%v", err)
	}
	if _, err := StreamSSHCommand(context.Background(), server, "internal-command", nil); err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("nil writer error=%v", err)
	}
	if _, err := (SSHRemote{}).StreamCommand(context.Background(), server, " ", io.Discard); err == nil || !strings.Contains(err.Error(), "command") {
		t.Fatalf("SSHRemote empty command error=%v", err)
	}
}

func (w cancelingWriter) Write(value []byte) (int, error) {
	w.cancel()
	return len(value), nil
}

func TestStreamSSHOutputCopiesBinaryBytes(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xff, '\n', 'A'}
	var dst bytes.Buffer

	copied, err := streamSSHOutputWithContext(context.Background(), &dst, bytes.NewReader(payload),
		func() error { return nil }, func() {}, func() string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if copied != int64(len(payload)) || !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("binary payload changed: %x", dst.Bytes())
	}
}

func TestStreamSSHOutputCancelsSessionWhenWriterFails(t *testing.T) {
	cancelled := make(chan struct{})
	started := make(chan struct{})

	copied, err := streamSSHOutputWithContext(context.Background(), closedPipeWriter{}, bytes.NewReader([]byte("data")),
		func() error {
			close(started)
			<-cancelled
			return errors.New("session closed")
		},
		func() { close(cancelled) },
		func() string { return "" },
	)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected closed pipe error, got %v", err)
	}
	if copied != 0 {
		t.Fatalf("copied = %d, want 0", copied)
	}
	select {
	case <-started:
	default:
		t.Fatal("expected session wait to start")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected cancel command to run")
	}
}

func TestStreamSSHOutputWaitsForBlockingWriterAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writerStarted := make(chan struct{}, 1)
	releaseWriter := make(chan struct{})
	cancelled := make(chan struct{})
	waitExited := make(chan struct{})
	streamReturned := make(chan struct{})
	resultCh := make(chan struct {
		copied int64
		err    error
	}, 1)

	go func() {
		copied, err := streamSSHOutputWithContext(ctx, blockingWriter{started: writerStarted, release: releaseWriter}, bytes.NewReader([]byte("data")),
			func() error {
				<-cancelled
				close(waitExited)
				return errors.New("session closed")
			},
			func() { close(cancelled) },
			func() string { return "" },
		)
		close(streamReturned)
		resultCh <- struct {
			copied int64
			err    error
		}{copied: copied, err: err}
	}()

	select {
	case <-writerStarted:
	case <-time.After(time.Second):
		t.Fatal("expected writer to block")
	}
	cancel()
	select {
	case <-waitExited:
	case <-time.After(time.Second):
		t.Fatal("expected context cancellation to close the session")
	}
	select {
	case <-streamReturned:
		t.Fatal("stream returned before writer unblocked")
	case <-time.After(2 * sshCancelDrainTimeout):
	}

	close(releaseWriter)
	select {
	case result := <-resultCh:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", result.err)
		}
		if result.copied != 4 {
			t.Fatalf("copied = %d, want 4", result.copied)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to finish after writer unblocked")
	}
}

func TestStreamSSHOutputCancelsWhenCopyCompletesWithPendingContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan struct{})
	resultCh := make(chan struct {
		copied int64
		err    error
	}, 1)

	go func() {
		copied, err := streamSSHOutputWithContext(ctx, cancelingWriter{cancel: cancel}, bytes.NewReader([]byte("data")),
			func() error {
				<-cancelled
				return errors.New("session closed")
			},
			func() { close(cancelled) },
			func() string { return "" },
		)
		resultCh <- struct {
			copied int64
			err    error
		}{copied: copied, err: err}
	}()

	select {
	case result := <-resultCh:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", result.err)
		}
		if result.copied != 4 {
			t.Fatalf("copied = %d, want 4", result.copied)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending context cancellation to close the session")
	}
}

func TestStreamSSHOutputPreservesWaitErrorUntilCopyCompletes(t *testing.T) {
	remoteErr := errors.New("remote command failed")
	writerStarted := make(chan struct{}, 1)
	releaseWriter := make(chan struct{})
	waitReturned := make(chan struct{})
	cancelled := make(chan struct{}, 1)
	resultCh := make(chan struct {
		copied int64
		err    error
	}, 1)

	go func() {
		copied, err := streamSSHOutputWithContext(context.Background(), blockingWriter{started: writerStarted, release: releaseWriter}, bytes.NewReader([]byte("data")),
			func() error {
				close(waitReturned)
				return remoteErr
			},
			func() { cancelled <- struct{}{} },
			func() string { return "" },
		)
		resultCh <- struct {
			copied int64
			err    error
		}{copied: copied, err: err}
	}()

	select {
	case <-writerStarted:
	case <-time.After(time.Second):
		t.Fatal("expected writer to block")
	}
	select {
	case <-waitReturned:
	case <-time.After(time.Second):
		t.Fatal("expected remote wait to return")
	}
	select {
	case result := <-resultCh:
		t.Fatalf("stream returned before copy completed: copied=%d err=%v", result.copied, result.err)
	default:
	}

	close(releaseWriter)
	select {
	case result := <-resultCh:
		if !errors.Is(result.err, remoteErr) {
			t.Fatalf("expected original remote error, got %v", result.err)
		}
		if result.copied != 4 {
			t.Fatalf("copied = %d, want 4", result.copied)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for copy to complete")
	}
	select {
	case <-cancelled:
		t.Fatal("remote wait error should not trigger a replacement cancellation error")
	default:
	}
}

func TestStreamSSHStderrKeepsWriteContractWhenTruncated(t *testing.T) {
	stderr := newBoundedSSHStderr(2)

	written, err := stderr.Write([]byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if written != 3 {
		t.Fatalf("written = %d, want 3", written)
	}
	if got := stderr.String(); got != "ab" {
		t.Fatalf("stderr = %q, want %q", got, "ab")
	}
}

func TestRunSSHCommandWithContextCancelsRunningSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	cancelled := make(chan struct{}, 1)
	started := make(chan struct{})

	startedAt := time.Now()
	err := runSSHCommandWithContext(ctx, func() {
		cancelled <- struct{}{}
	}, func() error {
		close(started)
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	elapsed := time.Since(startedAt)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline error, got %v", err)
	}
	if elapsed > 120*time.Millisecond {
		t.Fatalf("command helper waited for the slow session: %s", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("expected command function to start")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected cancel function to run")
	}
}

func TestRunSSHCommandWithContextWaitsBrieflyForCancelledRunToExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	done := make(chan struct{})
	resultCh := make(chan error, 1)

	go func() {
		resultCh <- runSSHCommandWithContext(ctx, func() {
			close(cancelled)
		}, func() error {
			close(started)
			<-cancelled
			time.Sleep(30 * time.Millisecond)
			close(done)
			return errors.New("session closed")
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected command function to start")
	}
	cancelStartedAt := time.Now()
	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for command helper")
	}
	if elapsed := time.Since(cancelStartedAt); elapsed < 25*time.Millisecond {
		t.Fatalf("helper returned before the cancelled run exited: %s", elapsed)
	}
	select {
	case <-done:
	default:
		t.Fatal("expected cancelled command function to exit before helper returns")
	}
}

func TestRunSSHUploadWithContextCancelsCopyAndRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	runDone := make(chan struct{})
	copyDone := make(chan struct{})
	resultCh := make(chan error, 1)

	go func() {
		resultCh <- runSSHUploadWithContext(ctx, func() {
			close(cancelled)
		}, func() error {
			close(started)
			<-cancelled
			close(runDone)
			return errors.New("session closed")
		}, func() error {
			<-cancelled
			time.Sleep(30 * time.Millisecond)
			close(copyDone)
			return errors.New("copy aborted")
		}, func() string {
			return "remote stderr"
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected upload command to start")
	}
	cancelStartedAt := time.Now()
	cancel()
	select {
	case err := <-resultCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upload helper")
	}
	if elapsed := time.Since(cancelStartedAt); elapsed < 25*time.Millisecond {
		t.Fatalf("upload helper returned before cancelled copy exited: %s", elapsed)
	}
	select {
	case <-runDone:
	default:
		t.Fatal("expected cancelled upload command to exit before helper returns")
	}
	select {
	case <-copyDone:
	default:
		t.Fatal("expected cancelled upload copy to exit before helper returns")
	}
}
