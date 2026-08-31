package uploadkit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type fakeRemote struct {
	local     string
	remote    string
	mode      os.FileMode
	err       error
	errs      []error
	uploads   int
	commands  []string
	runResult adapter.CommandResult
	runErr    error
	events    []string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.commands = append(f.commands, command)
	f.events = append(f.events, "verify")
	return f.runResult, f.runErr
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.local = localPath
	f.remote = remotePath
	f.mode = mode
	f.uploads++
	f.events = append(f.events, "upload")
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	return f.err
}

func TestUploadVerifiedProvesRemoteSHA256AndSizeAfterUpload(t *testing.T) {
	local := filepath.Join(t.TempDir(), "bundle.tar.gz")
	payload := []byte("verified-payload")
	if err := os.WriteFile(local, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	remote := &fakeRemote{runResult: adapter.CommandResult{
		Stdout: fmt.Sprintf("AIFAR_UPLOAD_VERIFY %x %d\n", sum, len(payload)),
	}}
	got, err := UploadVerified(context.Background(), remote, store.Server{}, VerifiedFile{
		File:      File{LocalPath: local, RemotePath: "/aifar/apps/admin/.aifar-lifecycle/install-r1/stage/bundle.tar.gz"},
		StageRoot: "/aifar/apps/admin/.aifar-lifecycle/install-r1/stage",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != fmt.Sprintf("%x", sum) || got.Size != int64(len(payload)) {
		t.Fatalf("unexpected verification: %+v", got)
	}
	if strings.Join(remote.events, ",") != "upload,verify" {
		t.Fatalf("events=%v", remote.events)
	}
}

func TestUploadVerifiedRejectsChecksumMismatchAndCommandsExactFileCleanup(t *testing.T) {
	local := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := os.WriteFile(local, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{runResult: adapter.CommandResult{Stdout: "AIFAR_UPLOAD_VERIFY " + strings.Repeat("0", 64) + " 7\n"}}
	_, err := UploadVerified(context.Background(), remote, store.Server{}, VerifiedFile{
		File: File{LocalPath: local, RemotePath: "/stage/file"}, StageRoot: "/stage",
	}, nil)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	if len(remote.commands) != 1 || !strings.Contains(remote.commands[0], `rm -f -- "$target"`) || !strings.Contains(remote.commands[0], "AIFAR_UPLOAD_VERIFY") {
		t.Fatalf("unexpected verification command: %q", remote.commands)
	}
	if strings.Contains(remote.commands[0], "rm -rf") || strings.Contains(remote.commands[0], "rm -r ") {
		t.Fatalf("recursive delete in command: %q", remote.commands[0])
	}
}

func TestUploadVerifiedRejectsSizeMismatch(t *testing.T) {
	local := filepath.Join(t.TempDir(), "bundle")
	if err := os.WriteFile(local, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("payload"))
	remote := &fakeRemote{runResult: adapter.CommandResult{Stdout: fmt.Sprintf("AIFAR_UPLOAD_VERIFY %x 8\n", sum)}}
	_, err := UploadVerified(context.Background(), remote, store.Server{}, VerifiedFile{File: File{LocalPath: local, RemotePath: "/stage/file"}, StageRoot: "/stage"}, nil)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestUploadVerifiedRejectsMalformedProof(t *testing.T) {
	local := filepath.Join(t.TempDir(), "bundle")
	if err := os.WriteFile(local, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{runResult: adapter.CommandResult{Stdout: "AIFAR_UPLOAD_VERIFY bad 7\n"}}
	_, err := UploadVerified(context.Background(), remote, store.Server{}, VerifiedFile{File: File{LocalPath: local, RemotePath: "/stage/file"}, StageRoot: "/stage"}, nil)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected verification failure, got %v", err)
	}
}

func TestUploadVerifiedRejectsRemoteVerificationFailureWithoutProof(t *testing.T) {
	local := filepath.Join(t.TempDir(), "bundle")
	if err := os.WriteFile(local, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{runErr: errors.New("remote failed")}
	_, err := UploadVerified(context.Background(), remote, store.Server{}, VerifiedFile{File: File{LocalPath: local, RemotePath: "/stage/file"}, StageRoot: "/stage"}, nil)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected verification failure, got %v", err)
	}
}

func TestUploadVerifiedRejectsPathOutsideStageBeforeUpload(t *testing.T) {
	local := filepath.Join(t.TempDir(), "bundle")
	if err := os.WriteFile(local, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{}
	_, err := UploadVerified(context.Background(), remote, store.Server{}, VerifiedFile{File: File{LocalPath: local, RemotePath: "/stage-escape/file"}, StageRoot: "/stage"}, nil)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected verification failure, got %v", err)
	}
	if remote.uploads != 0 || len(remote.commands) != 0 {
		t.Fatalf("remote was used: uploads=%d commands=%v", remote.uploads, remote.commands)
	}
}

type fakeLogger struct {
	message string
	args    []any
}

func (f *fakeLogger) Info(format string, args ...any) {
	f.message = format
	f.args = args
}

func (f *fakeLogger) Error(format string, args ...any) {}

func TestUploadLogsAndDefaultsMode(t *testing.T) {
	remote := &fakeRemote{}
	log := &fakeLogger{}
	err := Upload(context.Background(), remote, store.Server{}, File{
		LocalPath:      "local.tar",
		RemotePath:     "/remote/local.tar",
		LogMessage:     "upload %s",
		LogArgs:        []any{"local.tar"},
		FailureMessage: "upload failed",
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	if remote.mode != 0o644 {
		t.Fatalf("expected default 0644 mode, got %v", remote.mode)
	}
	if log.message != "upload %s" || len(log.args) != 1 || log.args[0] != "local.tar" {
		t.Fatalf("unexpected log call: %#v %#v", log.message, log.args)
	}
}

func TestUploadWrapsFailureMessage(t *testing.T) {
	remote := &fakeRemote{err: errors.New("denied")}
	err := Upload(context.Background(), remote, store.Server{}, File{
		LocalPath:      "local.tar",
		RemotePath:     "/remote/local.tar",
		FailureMessage: "upload %s failed",
		FailureArgs:    []any{"local.tar"},
	}, nil)
	if err == nil || err.Error() != "upload local.tar failed: denied" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadRetriesTransientFailure(t *testing.T) {
	oldDelay := uploadRetryDelay
	uploadRetryDelay = 0
	t.Cleanup(func() { uploadRetryDelay = oldDelay })

	remote := &fakeRemote{errs: []error{errors.New("EOF"), nil}}
	err := Upload(context.Background(), remote, store.Server{}, File{
		LocalPath:      "local.tar",
		RemotePath:     "/remote/local.tar",
		FailureMessage: "upload failed",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if remote.uploads != 2 {
		t.Fatalf("expected two upload attempts, got %d", remote.uploads)
	}
}

func TestUploadDoesNotRetryPermanentFailure(t *testing.T) {
	oldDelay := uploadRetryDelay
	uploadRetryDelay = 0
	t.Cleanup(func() { uploadRetryDelay = oldDelay })

	remote := &fakeRemote{err: errors.New("no space left on device")}
	err := Upload(context.Background(), remote, store.Server{}, File{
		LocalPath:      "local.tar",
		RemotePath:     "/remote/local.tar",
		FailureMessage: "upload failed",
	}, nil)
	if err == nil {
		t.Fatal("expected upload failure")
	}
	if remote.uploads != 1 {
		t.Fatalf("expected one upload attempt, got %d", remote.uploads)
	}
}

func TestRPMFilesBuildsRemotePaths(t *testing.T) {
	files := RPMFiles([]string{`C:\cache\a.rpm`, "/cache/b.rpm"}, "/work/rpms/", "upload %s", "upload %s failed")
	if len(files) != 2 {
		t.Fatalf("expected two files, got %d", len(files))
	}
	if files[0].RemotePath != "/work/rpms/a.rpm" || files[1].RemotePath != "/work/rpms/b.rpm" {
		t.Fatalf("unexpected remote paths: %#v", files)
	}
}
