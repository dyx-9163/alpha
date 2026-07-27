//go:build !windows && !linux

package backuprepo

import (
	"fmt"
	"os"
)

func atomicRenameNoReplace(oldpath, newpath string) error {
	info, err := os.Lstat(oldpath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("atomic non-clobber directory rename is unsupported on this platform")
	}
	if err := os.Link(oldpath, newpath); err != nil {
		return err
	}
	if err := os.Remove(oldpath); err != nil {
		_ = os.Remove(newpath)
		return err
	}
	return nil
}
