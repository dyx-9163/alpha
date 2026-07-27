//go:build windows

package aifar

import (
	"math"
	"syscall"

	"golang.org/x/sys/windows"
)

func runtimeDiagnosticAvailableBytes(root string) (int64, error) {
	rootPath, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(rootPath, &available, nil, nil); err != nil {
		return 0, err
	}
	if available > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(available), nil
}

func runtimeDiagnosticSyncDirectory(string) error {
	return nil
}
