//go:build windows

package backuprepo

import (
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func platformOpenRoot(path string) (*os.File, error) {
	return openWindowsDirectory(path)
}

func platformOpenDirectoryAt(parent *os.File, parentPath, name string) (*os.File, error) {
	if err := validateSingleName(name); err != nil {
		return nil, err
	}
	return openWindowsDirectory(filepath.Join(parentPath, name))
}

func openWindowsDirectory(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	return os.NewFile(uintptr(handle), path), nil
}

func platformOpenRegularAt(parent *os.File, parentPath, name string, flag int) (*os.File, error) {
	if err := validateSingleName(name); err != nil {
		return nil, err
	}
	path := filepath.Join(parentPath, name)
	pathName, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	if flag&os.O_RDWR != 0 {
		access |= windows.GENERIC_WRITE
	}
	handle, err := windows.CreateFile(pathName, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	return os.NewFile(uintptr(handle), path), nil
}

func platformCreateRegularAt(parent *os.File, parentPath, name string, mode os.FileMode) (*os.File, error) {
	if err := validateSingleName(name); err != nil {
		return nil, err
	}
	path := filepath.Join(parentPath, name)
	pathName, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(pathName, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func platformRenameNoReplaceAt(parent *os.File, parentPath, oldName, newName string) error {
	if err := validateSingleName(oldName); err != nil {
		return err
	}
	if err := validateSingleName(newName); err != nil {
		return err
	}
	oldPath, err := windows.UTF16PtrFromString(filepath.Join(parentPath, oldName))
	if err != nil {
		return err
	}
	newPath, err := windows.UTF16PtrFromString(filepath.Join(parentPath, newName))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(oldPath, newPath, 0)
}

func platformUnlinkOwnedAt(parent *os.File, parentPath, name string, expected os.FileInfo) error {
	if err := validateSingleName(name); err != nil {
		return err
	}
	path := filepath.Join(parentPath, name)
	file, err := openWindowsDeleteHandle(path, false)
	if err != nil {
		return err
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(expected, current) {
		return os.ErrPermission
	}
	return markWindowsHandleForDeletion(file)
}

func platformSealRegular(file *os.File) error {
	return nil
}

func platformRemoveTreeAt(parent *os.File, parentPath, name string, expected os.FileInfo) error {
	if err := validateSingleName(name); err != nil {
		return err
	}
	return removeWindowsTree(filepath.Join(parentPath, name), expected)
}

func removeWindowsTree(path string, expected os.FileInfo) error {
	directory, err := openWindowsDeleteHandle(path, true)
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(expected, info) || !info.IsDir() {
		return os.ErrPermission
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		childInfo, err := os.Lstat(child)
		if err != nil {
			return err
		}
		if childInfo.Mode()&os.ModeSymlink == 0 && childInfo.IsDir() {
			if err := removeWindowsTree(child, childInfo); err != nil {
				return err
			}
		} else if err := removeWindowsFile(child, childInfo); err != nil {
			return err
		}
	}
	return markWindowsHandleForDeletion(directory)
}

func removeWindowsFile(path string, expected os.FileInfo) error {
	file, err := openWindowsDeleteHandle(path, false)
	if err != nil {
		return err
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !os.SameFile(expected, current) {
		return os.ErrPermission
	}
	return markWindowsHandleForDeletion(file)
}

func openWindowsDeleteHandle(path string, directory bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.DELETE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func markWindowsHandleForDeletion(file *os.File) error {
	disposition := byte(1)
	return windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&disposition)), 1)
}
