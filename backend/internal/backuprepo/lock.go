package backuprepo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const repositoryLockName = ".aifar-repository.lock"

var repositoryRootMutexes sync.Map

type repositoryFileLease struct {
	root   stableObject
	lock   stableObject
	locked bool
}

func mutexForRepositoryRoot(root string) *sync.Mutex {
	key := filepath.Clean(root)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	value, _ := repositoryRootMutexes.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (r *Repository) withRepositoryLock(operation func() error) (retErr error) {
	if r == nil || r.mutex == nil {
		return errors.New("backup repository lock is not initialized")
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	lease, err := acquireRepositoryFileLock(r.root)
	if err != nil {
		return fmt.Errorf("acquire backup repository lock: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, lease.release())
	}()
	return operation()
}

func acquireRepositoryFileLock(rootPath string) (*repositoryFileLease, error) {
	rootFile, err := platformOpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	root, err := stableDirectoryFromFile(rootPath, rootFile)
	if err != nil {
		rootFile.Close()
		return nil, err
	}
	if err := platformValidateRootSecurity(root.info); err != nil {
		root.file.Close()
		return nil, err
	}
	lockFile, err := platformOpenLockAt(root.file, root.path, repositoryLockName, false)
	if errors.Is(err, os.ErrNotExist) {
		lockFile, err = platformOpenLockAt(root.file, root.path, repositoryLockName, true)
		if errors.Is(err, os.ErrExist) {
			lockFile, err = platformOpenLockAt(root.file, root.path, repositoryLockName, false)
		}
	}
	if err != nil {
		root.file.Close()
		return nil, err
	}
	lockInfo, err := lockFile.Stat()
	if err != nil {
		lockFile.Close()
		root.file.Close()
		return nil, err
	}
	lock := stableObject{path: filepath.Join(root.path, repositoryLockName), info: lockInfo, file: lockFile}
	if !lockInfo.Mode().IsRegular() || lockInfo.Mode()&os.ModeSymlink != 0 {
		lock.file.Close()
		root.file.Close()
		return nil, errors.New("backup repository lock is not a regular file")
	}
	if err := platformValidateLockSecurity(lockInfo); err != nil {
		lock.file.Close()
		root.file.Close()
		return nil, err
	}
	if err := platformTryExclusiveLock(lock.file); err != nil {
		lock.file.Close()
		root.file.Close()
		return nil, err
	}
	lease := &repositoryFileLease{root: root, lock: lock, locked: true}
	if err := recheckStableObject(root); err != nil {
		_ = lease.release()
		return nil, err
	}
	if err := recheckStableObject(lock); err != nil {
		_ = lease.release()
		return nil, err
	}
	return lease, nil
}

func (lease *repositoryFileLease) release() error {
	if lease == nil {
		return nil
	}
	var result error
	if lease.lock.file != nil {
		if lease.locked {
			result = errors.Join(result, recheckStableObject(lease.lock))
			result = errors.Join(result, platformUnlock(lease.lock.file))
			lease.locked = false
		}
		result = errors.Join(result, lease.lock.file.Close())
		lease.lock.file = nil
	}
	if lease.root.file != nil {
		result = errors.Join(result, recheckStableObject(lease.root))
		result = errors.Join(result, lease.root.file.Close())
		lease.root.file = nil
	}
	return result
}
