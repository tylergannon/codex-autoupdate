package runlock

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type Lock struct {
	file *os.File
}

func Acquire(directory string) (*Lock, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	path := filepath.Join(directory, "watcher.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open watcher lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if err == unix.EWOULDBLOCK {
			return nil, fmt.Errorf("another codex-autoupdate watcher is already running")
		}
		return nil, fmt.Errorf("lock watcher: %w", err)
	}
	if err := file.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN); err != nil {
		return fmt.Errorf("unlock watcher: %w", err)
	}
	return l.file.Close()
}
