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
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		return "", 0, fmt.Errorf("backup repository file %q changed at its managed path", path)
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
	current, err = os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(after, current) {
		return "", 0, fmt.Errorf("backup repository file %q changed after hashing", path)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func hashStableFile(object stableObject) (string, int64, error) {
	if object.file == nil || object.info == nil || !object.info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("backup repository file %q is not stably open", object.path)
	}
	if _, err := object.file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	before, err := object.file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(object.info, before) || !before.Mode().IsRegular() {
		return "", 0, fmt.Errorf("backup repository file %q changed before hashing", object.path)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, object.file)
	if err != nil {
		return "", 0, err
	}
	after, err := object.file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(before, after) || after.Size() != size {
		return "", 0, fmt.Errorf("backup repository file %q changed while hashing", object.path)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
