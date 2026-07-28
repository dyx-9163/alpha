//go:build !windows && !linux

package backuprepo

import (
	"fmt"
	"os"
)

func platformOpenRoot(path string) (*os.File, error) { return os.Open(path) }
func platformOpenDirectoryAt(parent *os.File, parentPath, name string) (*os.File, error) {
	return nil, fmt.Errorf("anchored child-directory open is unsupported on this platform")
}
func platformOpenRegularAt(parent *os.File, parentPath, name string, flag int) (*os.File, error) {
	return nil, fmt.Errorf("anchored regular-file open is unsupported on this platform")
}
func platformCreateRegularAt(parent *os.File, parentPath, name string, mode os.FileMode) (*os.File, error) {
	return nil, fmt.Errorf("anchored regular-file creation is unsupported on this platform")
}
func platformOpenLockAt(parent *os.File, parentPath, name string, create bool) (*os.File, error) {
	return nil, fmt.Errorf("anchored repository locking is unsupported on this platform")
}
func platformTryExclusiveLock(file *os.File) error {
	return fmt.Errorf("repository locking is unsupported on this platform")
}
func platformUnlock(file *os.File) error {
	return fmt.Errorf("repository unlocking is unsupported on this platform")
}
func platformValidateRootSecurity(info os.FileInfo) error {
	return fmt.Errorf("repository root security validation is unsupported on this platform")
}
func platformValidateLockSecurity(info os.FileInfo) error {
	return fmt.Errorf("repository lock security validation is unsupported on this platform")
}
func platformRequireSingleLink(file *os.File) error {
	return fmt.Errorf("hard-link validation is unsupported on this platform")
}
func platformRenameNoReplaceAt(parent *os.File, parentPath, oldName, newName string) error {
	return fmt.Errorf("anchored atomic non-clobber rename is unsupported on this platform")
}
func platformUnlinkOwnedAt(parent *os.File, parentPath, name string, expected os.FileInfo) error {
	return fmt.Errorf("anchored unlink is unsupported on this platform")
}
func platformSealRegular(file *os.File) error {
	return fmt.Errorf("archive sealing is unsupported on this platform")
}
func platformRemoveTreeAt(parent *os.File, parentPath, name string, expected os.FileInfo) error {
	return fmt.Errorf("anchored recursive deletion is unsupported on this platform")
}
