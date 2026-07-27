//go:build !windows

package aifar

import (
	"math"
	"os"

	"golang.org/x/sys/unix"
)

func runtimeDiagnosticAvailableBytes(root string) (int64, error) {
	var stats unix.Statfs_t
	if err := unix.Statfs(root, &stats); err != nil {
		return 0, err
	}
	available := uint64(stats.Bavail) * uint64(stats.Bsize)
	if available > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(available), nil
}

func runtimeDiagnosticSyncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
