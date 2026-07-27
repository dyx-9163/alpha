//go:build windows

package backuprepo

import "golang.org/x/sys/windows"

func atomicRenameNoReplace(oldpath, newpath string) error {
	oldname, err := windows.UTF16PtrFromString(oldpath)
	if err != nil {
		return err
	}
	newname, err := windows.UTF16PtrFromString(newpath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(oldname, newname, 0)
}
