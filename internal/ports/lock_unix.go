//go:build darwin || linux || freebsd || openbsd || netbsd

package ports

import (
	"os"
	"syscall"
)

// withLockedFile holds an exclusive flock on a sidecar .lock file while fn
// runs. The lock is advisory but honored by all forktrust processes, so
// concurrent `forktrust new` invocations serialize their port allocation.
func withLockedFile(storePath string, fn func() error) error {
	if err := EnsureLockDir(storePath); err != nil {
		return err
	}
	lockPath := storePath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
