package runlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	ErrAlreadyRunning = errors.New("another codex-autoupdate watcher is already running")
	ErrYieldRequested = errors.New("yielding to a one-shot codex-autoupdate command")
)

const takeoverName = "takeover.request"

type Lock struct {
	file *os.File
}

type Takeover struct {
	path string
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
			return nil, ErrAlreadyRunning
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

func RequestTakeover(directory string, wake func(int) error) (*Takeover, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create takeover directory: %w", err)
	}
	holderPID, err := readPID(filepath.Join(directory, "watcher.lock"))
	if err != nil {
		return nil, fmt.Errorf("read watcher PID: %w", err)
	}
	path := filepath.Join(directory, takeoverName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("another one-shot codex-autoupdate command is already waiting")
		}
		return nil, fmt.Errorf("create takeover request: %w", err)
	}
	request := &Takeover{path: path}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		_ = request.Close()
		return nil, fmt.Errorf("write takeover request: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = request.Close()
		return nil, fmt.Errorf("close takeover request: %w", err)
	}
	if wake == nil {
		wake = func(pid int) error { return syscall.Kill(pid, syscall.SIGUSR1) }
	}
	if err := wake(holderPID); err != nil {
		_ = request.Close()
		return nil, fmt.Errorf("wake running watcher PID %d: %w", holderPID, err)
	}
	return request, nil
}

func TakeoverRequested(directory string) (bool, error) {
	path := filepath.Join(directory, takeoverName)
	requesterPID, err := readPID(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("read takeover request: %w", err)
	}
	if err := syscall.Kill(requesterPID, 0); err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	} else if !errors.Is(err, syscall.ESRCH) {
		return true, fmt.Errorf("inspect takeover requester PID %d: %w", requesterPID, err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return true, fmt.Errorf("remove stale takeover request: %w", err)
	}
	return false, nil
}

func (t *Takeover) Close() error {
	if t == nil || t.path == "" {
		return nil
	}
	if err := os.Remove(t.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove takeover request: %w", err)
	}
	t.path = ""
	return nil
}

func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid PID in %s", path)
	}
	return pid, nil
}
