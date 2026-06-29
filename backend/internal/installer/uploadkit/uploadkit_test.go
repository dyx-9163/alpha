package uploadkit

import (
	"context"
	"errors"
	"os"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type fakeRemote struct {
	local  string
	remote string
	mode   os.FileMode
	err    error
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	return adapter.CommandResult{}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.local = localPath
	f.remote = remotePath
	f.mode = mode
	return f.err
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

func TestRPMFilesBuildsRemotePaths(t *testing.T) {
	files := RPMFiles([]string{`C:\cache\a.rpm`, "/cache/b.rpm"}, "/work/rpms/", "upload %s", "upload %s failed")
	if len(files) != 2 {
		t.Fatalf("expected two files, got %d", len(files))
	}
	if files[0].RemotePath != "/work/rpms/a.rpm" || files[1].RemotePath != "/work/rpms/b.rpm" {
		t.Fatalf("unexpected remote paths: %#v", files)
	}
}
