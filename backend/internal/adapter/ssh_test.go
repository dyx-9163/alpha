package adapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

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
