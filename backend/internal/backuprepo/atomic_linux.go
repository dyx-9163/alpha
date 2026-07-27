//go:build linux

package backuprepo

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func platformOpenRoot(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func platformOpenDirectoryAt(parent *os.File, parentPath, name string) (*os.File, error) {
	if err := validateSingleName(name); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(parentPath, name)), nil
}

func platformOpenRegularAt(parent *os.File, parentPath, name string, flag int) (*os.File, error) {
	if err := validateSingleName(name); err != nil {
		return nil, err
	}
	unixFlag := unix.O_RDONLY
	if flag&os.O_RDWR != 0 {
		unixFlag = unix.O_RDWR
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unixFlag|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(parentPath, name)), nil
}

func platformCreateRegularAt(parent *os.File, parentPath, name string, mode os.FileMode) (*os.File, error) {
	if err := validateSingleName(name); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(parentPath, name)), nil
}

func platformRenameNoReplaceAt(parent *os.File, parentPath, oldName, newName string) error {
	if err := validateSingleName(oldName); err != nil {
		return err
	}
	if err := validateSingleName(newName); err != nil {
		return err
	}
	return unix.Renameat2(int(parent.Fd()), oldName, int(parent.Fd()), newName, unix.RENAME_NOREPLACE)
}

func platformUnlinkOwnedAt(parent *os.File, parentPath, name string, expected os.FileInfo) error {
	if err := validateSingleName(name); err != nil {
		return err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), filepath.Join(parentPath, name))
	current, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	if !os.SameFile(expected, current) {
		file.Close()
		return fmt.Errorf("refusing to unlink changed managed object %q", name)
	}
	err = unix.Unlinkat(int(parent.Fd()), name, 0)
	file.Close()
	return err
}

func platformSealRegular(file *os.File) error {
	if err := unix.Fchmod(int(file.Fd()), 0o400); err != nil {
		return err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("lock promoted backup archive: %w", err)
	}
	return nil
}

func platformRemoveTreeAt(parent *os.File, parentPath, name string, expected os.FileInfo) error {
	directory, err := platformOpenDirectoryAt(parent, parentPath, name)
	if err != nil {
		return err
	}
	current, err := directory.Stat()
	if err != nil || !os.SameFile(expected, current) {
		directory.Close()
		return fmt.Errorf("refusing to remove changed managed directory %q", name)
	}
	return removeLinuxOpenedTree(parent, parentPath, name, directory)
}

func removeLinuxOpenedTree(parent *os.File, parentPath, name string, directory *os.File) error {
	entries, readErr := directory.ReadDir(-1)
	if readErr != nil {
		directory.Close()
		return readErr
	}
	for _, entry := range entries {
		child := entry.Name()
		if err := validateSingleName(child); err != nil {
			directory.Close()
			return err
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(int(directory.Fd()), child, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			directory.Close()
			return err
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			childDirectory, err := platformOpenDirectoryAt(directory, filepath.Join(parentPath, name), child)
			if err != nil {
				directory.Close()
				return err
			}
			var opened unix.Stat_t
			if err := unix.Fstat(int(childDirectory.Fd()), &opened); err != nil || opened.Dev != stat.Dev || opened.Ino != stat.Ino {
				childDirectory.Close()
				directory.Close()
				return fmt.Errorf("managed child directory %q changed before recursive deletion", child)
			}
			if err := removeLinuxOpenedTree(directory, filepath.Join(parentPath, name), child, childDirectory); err != nil {
				directory.Close()
				return err
			}
		} else {
			fd, err := unix.Openat(int(directory.Fd()), child, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				directory.Close()
				return err
			}
			var opened unix.Stat_t
			if err := unix.Fstat(fd, &opened); err != nil || opened.Dev != stat.Dev || opened.Ino != stat.Ino {
				unix.Close(fd)
				directory.Close()
				return fmt.Errorf("managed child %q changed before unlink", child)
			}
			if err := unix.Unlinkat(int(directory.Fd()), child, 0); err != nil {
				unix.Close(fd)
				directory.Close()
				return err
			}
			unix.Close(fd)
		}
	}
	if err := directory.Close(); err != nil {
		return err
	}
	return unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
}
