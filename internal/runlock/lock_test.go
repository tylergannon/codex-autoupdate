package runlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTakeoverRequestWakesHolderAndPersistsUntilClosed(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	lock, err := AcquireDaemon(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	wokenPID := 0
	request, err := RequestTakeover(directory, func(pid int) error {
		wokenPID = pid
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if wokenPID != os.Getpid() {
		t.Fatalf("woken PID = %d, want %d", wokenPID, os.Getpid())
	}
	requested, err := TakeoverRequested(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !requested {
		t.Fatal("takeover request was not visible")
	}
	if err := request.Close(); err != nil {
		t.Fatal(err)
	}
	requested, err = TakeoverRequested(directory)
	if err != nil {
		t.Fatal(err)
	}
	if requested {
		t.Fatal("closed takeover request remained visible")
	}
}

func TestTakeoverDoesNotSignalOneShotHolder(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	lock, err := Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	signaled := false
	request, err := RequestTakeover(directory, func(int) error {
		signaled = true
		return nil
	})
	if err == nil {
		_ = request.Close()
		t.Fatal("expected one-shot contention error")
	}
	if signaled {
		t.Fatal("one-shot holder received takeover signal")
	}
	if _, statErr := os.Stat(filepath.Join(directory, takeoverName)); !os.IsNotExist(statErr) {
		t.Fatalf("one-shot contention created takeover marker: %v", statErr)
	}
}

func TestTakeoverRequestedRemovesStaleRequester(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, takeoverName)
	if err := os.WriteFile(path, []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requested, err := TakeoverRequested(directory)
	if err != nil {
		t.Fatal(err)
	}
	if requested {
		t.Fatal("stale takeover request remained active")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale request was not removed: %v", err)
	}
}
