package installerkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type fakeRemote struct {
	result adapter.CommandResult
	err    error
}

func (f fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	return f.result, f.err
}

func (f fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	return nil
}

type fakeLogger struct {
	infos  []string
	errors []string
}

func (l *fakeLogger) Info(format string, args ...any) {
	l.infos = append(l.infos, fmt.Sprintf(format, args...))
}

func (l *fakeLogger) Error(format string, args ...any) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

func TestRunLogsSuccessfulStderrAsInfo(t *testing.T) {
	log := &fakeLogger{}
	_, err := Run(context.Background(), fakeRemote{
		result: adapter.CommandResult{Stdout: "ok\n", Stderr: "warning\n"},
	}, store.Server{}, "true", log, "failed")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(log.infos, "\n") != "ok\nwarning" {
		t.Fatalf("unexpected info logs: %+v", log.infos)
	}
	if len(log.errors) != 0 {
		t.Fatalf("unexpected error logs: %+v", log.errors)
	}
}

func TestRunWrapsCommandErrors(t *testing.T) {
	log := &fakeLogger{}
	_, err := Run(context.Background(), fakeRemote{
		result: adapter.CommandResult{Stderr: "denied\n"},
		err:    errors.New("exit 1"),
	}, store.Server{}, "false", log, "custom failed")
	if err == nil || !strings.Contains(err.Error(), "custom failed: exit 1") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(log.errors, "\n") != "denied" {
		t.Fatalf("unexpected error logs: %+v", log.errors)
	}
}

func TestPathHelpers(t *testing.T) {
	if got := RemoteDeployDir(""); got != "/aifar/apps" {
		t.Fatalf("unexpected default deploy dir: %s", got)
	}
	if got := RemoteDeployDir("aifar/apps/"); got != "/aifar/apps" {
		t.Fatalf("unexpected normalized deploy dir: %s", got)
	}
	if got := WorkDir("/opt/aifar", "my app", "1/2", time.Unix(42, 0)); got != "/opt/aifar/_work/my-app-1-2-42" {
		t.Fatalf("unexpected work dir: %s", got)
	}
	if got := ShellQuote("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("unexpected shell quote: %s", got)
	}
}

func TestWriteTempScript(t *testing.T) {
	name, err := WriteTempScript("aifar-test-*.sh", "echo ok")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(name)
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "echo ok" {
		t.Fatalf("unexpected script: %s", data)
	}
}
