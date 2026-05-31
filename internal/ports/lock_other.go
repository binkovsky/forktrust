//go:build !(darwin || linux || freebsd || openbsd || netbsd)

package ports

// On platforms without flock support, fall back to running fn without a
// lock. Concurrent forktrust invocations may race on ports.json. This is
// a known limitation on Windows; the typical user runs one CLI process at
// a time so the race is unlikely in practice.
func withLockedFile(storePath string, fn func() error) error {
	if err := EnsureLockDir(storePath); err != nil {
		return err
	}
	return fn()
}
