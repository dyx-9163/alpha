package backuprepo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func fileSHA256(path string) (string, int64, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !before.Mode().IsRegular() {
		return "", 0, fmt.Errorf("backup repository file %q is not regular", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return "", 0, fmt.Errorf("backup repository file %q changed while opening", path)
	}

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	after, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(opened, after) || after.Size() != size {
		return "", 0, fmt.Errorf("backup repository file %q changed while hashing", path)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
