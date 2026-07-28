//go:build linux

package adapter

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRemoteDownloadHelperRejectsFinalSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.tar")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(directory, "dump.tar")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create final symlink: %v", err)
	}

	output, err := runRemoteDownloadHelper(link)
	if err == nil {
		t.Fatal("helper accepted a final symlink")
	}
	if len(output) != 0 {
		t.Fatalf("helper streamed final symlink bytes: %q", output)
	}
}

func TestRemoteDownloadHelperRejectsSymlinkedParent(t *testing.T) {
	directory := t.TempDir()
	realParent := filepath.Join(directory, "real-task")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatalf("create real parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realParent, "dump.tar"), []byte("outside"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	linkedParent := filepath.Join(directory, "task-123")
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatalf("create parent symlink: %v", err)
	}

	output, err := runRemoteDownloadHelper(filepath.Join(linkedParent, "dump.tar"))
	if err == nil {
		t.Fatal("helper accepted a symlinked parent")
	}
	if len(output) != 0 {
		t.Fatalf("helper streamed bytes through a symlinked parent: %q", output)
	}
}

func TestRemoteDownloadHelperRejectsNonRegularDescriptor(t *testing.T) {
	output, err := runRemoteDownloadHelper(t.TempDir())
	if err == nil {
		t.Fatal("helper accepted a directory descriptor")
	}
	if len(output) != 0 {
		t.Fatalf("helper streamed a non-regular descriptor: %q", output)
	}
}

func TestRemoteDownloadHelperStreamsFromOneOpenedDescriptor(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required by the production remote helper")
	}
	directory := t.TempDir()
	archivePath := filepath.Join(directory, "dump.tar")
	movedPath := filepath.Join(directory, "opened.tar")
	original := bytes.Repeat([]byte("A"), 4<<20)
	replacement := bytes.Repeat([]byte("B"), len(original))
	if err := os.WriteFile(archivePath, original, 0o600); err != nil {
		t.Fatalf("write original archive: %v", err)
	}

	command := exec.Command(python, "-c", remoteDownloadHelper, archivePath)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("open helper stdout: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	first := make([]byte, 1)
	if _, err := io.ReadFull(stdout, first); err != nil {
		_ = command.Wait()
		t.Fatalf("read first streamed byte: %v", err)
	}
	if err := os.Rename(archivePath, movedPath); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("rename opened archive: %v", err)
	}
	if err := os.WriteFile(archivePath, replacement, 0o600); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("write replacement archive: %v", err)
	}
	rest, readErr := io.ReadAll(stdout)
	waitErr := command.Wait()
	if readErr != nil {
		t.Fatalf("read helper output: %v", readErr)
	}
	if waitErr != nil {
		t.Fatalf("helper failed: %v", waitErr)
	}
	got := append(first, rest...)
	if !bytes.Equal(got, original) {
		t.Fatalf("helper reopened the replaced path: got %d bytes with first mismatch", len(got))
	}
}

func runRemoteDownloadHelper(path string) ([]byte, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, err
	}
	return exec.Command(python, "-c", remoteDownloadHelper, path).Output()
}
