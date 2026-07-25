package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tylergannon/codex-autoupdate/internal/runlock"
)

func TestDefaultHarnessSelectionAndFiltering(t *testing.T) {
	t.Parallel()
	if got := (settings{}).selectedHarnesses(); len(got) != 2 || got[0] != chatGPT || got[1] != claude {
		t.Fatalf("default harnesses = %v", got)
	}
	got := (settings{harnesses: []string{"claude", "claude"}}).selectedHarnesses()
	if len(got) != 1 || got[0] != claude {
		t.Fatalf("filtered harnesses = %v", got)
	}
}

func TestRunOnceAndForceSilentlySkipMissingApplications(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"run", "--once"},
		{"update", "--force"},
		{"update", "--force", "--harness", "claude"},
	} {
		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		root, err := NewRoot("test", stdout, stderr)
		if err != nil {
			t.Fatal(err)
		}
		temp := t.TempDir()
		common := []string{
			"--chatgpt-app-path", filepath.Join(temp, "ChatGPT.app"),
			"--claude-app-path", filepath.Join(temp, "Claude.app"),
			"--codex-home", filepath.Join(temp, ".codex"),
			"--claude-data", filepath.Join(temp, "Claude"),
			"--cache-dir", filepath.Join(temp, "cache"),
		}
		root.SetArgs(append(common, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, stderr)
		}
	}
}

func TestCheckJSONReportsEachMissingHarness(t *testing.T) {
	t.Parallel()
	stdout := new(bytes.Buffer)
	root, err := NewRoot("test", stdout, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	root.SetArgs([]string{
		"--chatgpt-app-path", filepath.Join(temp, "ChatGPT.app"),
		"--claude-app-path", filepath.Join(temp, "Claude.app"),
		"--codex-home", filepath.Join(temp, ".codex"),
		"--claude-data", filepath.Join(temp, "Claude"),
		"--cache-dir", filepath.Join(temp, "cache"),
		"check", "--json",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Harnesses []checkResult `json:"harnesses"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Harnesses) != 2 || payload.Harnesses[0].Installed || payload.Harnesses[1].Installed {
		t.Fatalf("unexpected check results: %+v", payload.Harnesses)
	}
}

func TestValidateRejectsUnknownHarness(t *testing.T) {
	t.Parallel()
	config := settings{
		chatGPTAppPath:       "/Applications/ChatGPT.app",
		claudeAppPath:        "/Applications/Claude.app",
		codexHome:            "/tmp/.codex",
		claudeData:           "/tmp/Claude",
		cacheDir:             "/tmp/cache",
		harnesses:            []string{"other"},
		idleWindow:           1,
		pollInterval:         1,
		activityPollInterval: 1,
		quitTimeout:          1,
		launchTimeout:        1,
	}
	if err := config.validate(); err == nil {
		t.Fatal("expected unknown harness error")
	}
}

func TestOneShotRunRequestsSafeTakeoverToAcquireSharedLock(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	held, err := runlock.AcquireDaemon(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	lock, takeover, err := acquireRunLock(context.Background(), cacheDir, true, func(int) error {
		return held.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = lock.Close()
		_ = takeover.Close()
	}()
}

func TestContinuousRunDoesNotStopAgentOnLockContention(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	held, err := runlock.Acquire(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	_, takeover, err := acquireRunLock(context.Background(), cacheDir, false, nil)
	if !errors.Is(err, runlock.ErrAlreadyRunning) {
		t.Fatalf("expected already-running error, got %v", err)
	}
	if takeover != nil {
		t.Fatal("continuous run created a takeover")
	}
}
