package backuprepo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSHA256StreamsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dump.tar")
	content := []byte("controlled mysql backup archive")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	digest, size, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)
	if digest != hex.EncodeToString(want[:]) || size != int64(len(content)) {
		t.Fatalf("fileSHA256() = (%q, %d), want (%q, %d)", digest, size, hex.EncodeToString(want[:]), len(content))
	}
}

func TestFileSHA256RejectsNonRegularFile(t *testing.T) {
	if _, _, err := fileSHA256(t.TempDir()); err == nil {
		t.Fatal("fileSHA256 accepted a directory")
	}
}
