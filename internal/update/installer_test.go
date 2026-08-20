package update

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/appcast"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
)

func TestPrepareDownloadsExtractsVerifiesAndStages(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	writeFakeBundle(t, appPath, "1.0", 1)
	source := filepath.Join(root, "source", "ChatGPT.app")
	writeFakeBundle(t, source, "2.0", 2)
	archivePath := filepath.Join(root, "source.zip")
	if output, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--keepParent", source, archivePath).CombinedOutput(); err != nil {
		t.Fatalf("create fixture archive: %v: %s", err, output)
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(archive)
	}))
	defer server.Close()
	runner := &fixtureRunner{appPath: appPath}
	staleStage := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-1.new")
	staleFailure := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-failed-1-123")
	writeFakeBundle(t, staleStage, "1.0", 1)
	writeFakeBundle(t, staleFailure, "1.0", 1)
	installer := Installer{
		AppPath:       appPath,
		CacheDir:      filepath.Join(root, "cache"),
		QuitTimeout:   time.Second,
		LaunchTimeout: time.Second,
		HTTPClient:    server.Client(),
		Runner:        runner,
	}
	release := appcast.Release{Build: "2", Version: "2.0", URL: server.URL, Length: int64(len(archive))}
	prepared, err := installer.Prepare(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StagedPath != installer.stagedPath("2") {
		t.Fatalf("unexpected staged path: %s", prepared.StagedPath)
	}
	if build := readBuild(t, prepared.StagedPath); build != "2" {
		t.Fatalf("staged build %s, want 2", build)
	}
	for _, residue := range []string{staleStage, staleFailure} {
		if _, err := os.Stat(residue); !os.IsNotExist(err) {
			t.Fatalf("residue was not removed: %s", residue)
		}
	}
}

func TestApplyAtomicallyReplacesAndWaitsForApplication(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Second, Runner: runner}
	prepared := Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}
	preflightCalled := false
	if err := installer.Apply(context.Background(), prepared, func(context.Context) error {
		preflightCalled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !preflightCalled {
		t.Fatal("preflight was not called")
	}
	if build := readBuild(t, appPath); build != "2" {
		t.Fatalf("installed build %s, want 2", build)
	}
	backups, err := filepath.Glob(filepath.Join(root, ".ChatGPT.app.codex-autoupdate-backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("rollback bundle was not cleaned up: %v", backups)
	}
}

func TestRecoverInterruptedActivationRestoresAndRelaunchesBackup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	backupPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-backup-1-123")
	writeFakeBundle(t, backupPath, "1.0", 1)
	runner := &fixtureRunner{appPath: appPath}
	installer := Installer{
		AppPath:       appPath,
		CacheDir:      filepath.Join(root, "cache"),
		QuitTimeout:   time.Second,
		LaunchTimeout: time.Second,
		Runner:        runner,
	}
	recovered, err := installer.RecoverInterruptedActivation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("interrupted activation was not recovered")
	}
	if build := readBuild(t, appPath); build != "1" {
		t.Fatalf("restored build = %s, want 1", build)
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Fatalf("backup remained after recovery: %v", err)
	}
	runner.mu.Lock()
	commands := append([]string(nil), runner.commands...)
	runner.mu.Unlock()
	if fmt.Sprint(commands) != fmt.Sprint([]string{"/usr/bin/open"}) {
		t.Fatalf("recovery commands = %v, want relaunch", commands)
	}
}

func TestRecoverInterruptedActivationRefusesAmbiguousBackups(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	writeFakeBundle(t, filepath.Join(root, ".ChatGPT.app.codex-autoupdate-backup-1-123"), "1.0", 1)
	writeFakeBundle(t, filepath.Join(root, ".ChatGPT.app.codex-autoupdate-backup-1-456"), "1.0", 1)
	installer := Installer{
		AppPath:       appPath,
		CacheDir:      filepath.Join(root, "cache"),
		QuitTimeout:   time.Second,
		LaunchTimeout: time.Second,
		Runner:        &fixtureRunner{appPath: appPath},
	}
	recovered, err := installer.RecoverInterruptedActivation(context.Background())
	if err == nil || !strings.Contains(err.Error(), "found 2 rollback bundles") {
		t.Fatalf("expected ambiguous recovery error, got recovered=%t err=%v", recovered, err)
	}
}

func TestApplyUsesSIGTERMWhenScheduledTasksRefuseNormalQuit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, launched: true, quitRefused: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Second, Runner: runner}

	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if build := readBuild(t, appPath); build != "2" {
		t.Fatalf("installed build %s, want 2", build)
	}
	runner.mu.Lock()
	commands := append([]string(nil), runner.commands...)
	runner.mu.Unlock()
	want := []string{"/usr/bin/osascript", "/bin/kill -TERM 123", "/usr/bin/open"}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("shutdown command sequence %v, want %v", commands, want)
	}
}

func TestApplyUsesSIGTERMWhenGracefulQuitReturnsButApplicationStaysRunning(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, launched: true, quitIgnored: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Nanosecond, LaunchTimeout: time.Second, Runner: runner}

	if err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	commands := append([]string(nil), runner.commands...)
	runner.mu.Unlock()
	want := []string{"/usr/bin/osascript", "/bin/kill -TERM 123", "/usr/bin/open"}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("shutdown command sequence %v, want %v", commands, want)
	}
}

func TestApplyStopsEveryBundleProcessBeforeReplacement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "Claude.app")
	stagedPath := filepath.Join(root, ".Claude.app.codex-autoupdate-2.new")
	writeFakeBundleFor(t, appPath, "1.0", "1", macos.ClaudeIdentity)
	writeFakeBundleFor(t, stagedPath, "2.0", "2", macos.ClaudeIdentity)
	runner := &fixtureRunner{
		appPath:     appPath,
		identity:    macos.ClaudeIdentity,
		launched:    true,
		quitRefused: true,
		helpers:     map[int]bool{122: false, 124: true, 125: false},
	}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Nanosecond, LaunchTimeout: time.Second, Runner: runner, Identity: macos.ClaudeIdentity}

	if err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	commands := append([]string(nil), runner.commands...)
	remainingHelpers := len(runner.helpers)
	runner.mu.Unlock()
	want := []string{"/usr/bin/osascript", "/bin/kill -TERM 122 123 124 125", "/bin/kill -KILL 124", "/usr/bin/open"}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("shutdown command sequence %v, want %v", commands, want)
	}
	if remainingHelpers != 0 {
		t.Fatalf("%d old bundle helpers remain after replacement", remainingHelpers)
	}
}

func TestApplyRefusesReplacementWhenBundleProcessRespawns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, launched: true, respawnOnPSCall: 3}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Second, Runner: runner}

	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to move ChatGPT Desktop bundle") {
		t.Fatalf("expected replacement refusal after process respawn, got %v", err)
	}
	if build := readBuild(t, appPath); build != "1" {
		t.Fatalf("installed build %s after process respawn, want 1", build)
	}
	if build := readBuild(t, stagedPath); build != "2" {
		t.Fatalf("staged build %s after process respawn, want 2", build)
	}
}

func TestShutdownQuiescesRealBundleProcessTree(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("process-level bundle fixture requires macOS")
	}
	root := t.TempDir()
	appPath := filepath.Join(root, "Fixture.app")
	paths := map[string]string{
		"main":     filepath.Join(appPath, "Contents", "MacOS", "Fixture"),
		"normal":   filepath.Join(appPath, "Contents", "Helpers", "normal-child"),
		"stubborn": filepath.Join(appPath, "Contents", "Helpers", "stubborn-child"),
		"detached": filepath.Join(appPath, "Contents", "Helpers", "detached-helper"),
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(executable, path); err != nil {
			t.Fatalf("link process fixture %s: %v", path, err)
		}
	}
	readyDir := filepath.Join(root, "ready")
	if err := os.MkdirAll(readyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(paths["main"], "-test.run=^TestBundleProcessFixtureProcess$")
	command.Env = append(os.Environ(),
		"CODEX_AUTOUPDATE_FIXTURE_ROLE=main",
		"CODEX_AUTOUPDATE_FIXTURE_READY_DIR="+readyDir,
		"CODEX_AUTOUPDATE_FIXTURE_NORMAL="+paths["normal"],
		"CODEX_AUTOUPDATE_FIXTURE_STUBBORN="+paths["stubborn"],
		"CODEX_AUTOUPDATE_FIXTURE_DETACHED="+paths["detached"],
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finder := macos.ProcessFinder{}
	defer func() {
		processes, _ := finder.BundleProcesses(context.Background(), appPath)
		for _, process := range processes {
			_ = syscall.Kill(process.PID, syscall.SIGKILL)
		}
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		processes, findErr := finder.BundleProcesses(context.Background(), appPath)
		ready := true
		for _, role := range []string{"main", "normal", "stubborn", "detached"} {
			if _, statErr := os.Stat(filepath.Join(readyDir, role)); statErr != nil {
				ready = false
			}
		}
		if findErr == nil && ready && len(processes) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not start: processes=%+v error=%v ready=%t", processes, findErr, ready)
		}
		time.Sleep(25 * time.Millisecond)
	}

	runner := &quitRefusalExecRunner{}
	installer := Installer{
		AppPath:     appPath,
		QuitTimeout: 100 * time.Millisecond,
		Runner:      runner,
		Identity: macos.Identity{
			Name:             "Fixture Desktop",
			BundleIdentifier: "com.example.codex-autoupdate-fixture",
			Executable:       "Fixture",
		},
	}
	if err := installer.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	processes, err := finder.BundleProcesses(context.Background(), appPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 0 {
		t.Fatalf("old bundle processes remain after shutdown: %+v", processes)
	}
	runner.mu.Lock()
	signals := append([]string(nil), runner.signals...)
	runner.mu.Unlock()
	if fmt.Sprint(signals) != "[TERM KILL]" {
		t.Fatalf("shutdown signals = %v, want TERM then KILL", signals)
	}
	if err := os.Rename(appPath, appPath+".quiescent"); err != nil {
		t.Fatalf("move quiescent fixture bundle: %v", err)
	}
	_ = command.Wait()
}

func TestBundleProcessFixtureProcess(t *testing.T) {
	role := os.Getenv("CODEX_AUTOUPDATE_FIXTURE_ROLE")
	if role == "" {
		return
	}
	readyDir := os.Getenv("CODEX_AUTOUPDATE_FIXTURE_READY_DIR")
	if role == "main" {
		children := []struct {
			role     string
			path     string
			detached bool
		}{
			{role: "normal", path: os.Getenv("CODEX_AUTOUPDATE_FIXTURE_NORMAL")},
			{role: "stubborn", path: os.Getenv("CODEX_AUTOUPDATE_FIXTURE_STUBBORN")},
			{role: "detached", path: os.Getenv("CODEX_AUTOUPDATE_FIXTURE_DETACHED"), detached: true},
		}
		for _, child := range children {
			command := exec.Command(child.path, "-test.run=^TestBundleProcessFixtureProcess$")
			command.Env = append(os.Environ(), "CODEX_AUTOUPDATE_FIXTURE_ROLE="+child.role)
			if child.detached {
				command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			}
			if err := command.Start(); err != nil {
				os.Exit(2)
			}
		}
	}
	if role == "stubborn" || role == "detached" {
		signal.Ignore(syscall.SIGTERM)
	}
	if err := os.WriteFile(filepath.Join(readyDir, role), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		os.Exit(3)
	}
	for {
		time.Sleep(time.Hour)
	}
}

type quitRefusalExecRunner struct {
	mu      sync.Mutex
	signals []string
}

func (r *quitRefusalExecRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "/usr/bin/osascript" {
		return []byte("fixture refused normal quit"), fmt.Errorf("exit status 1")
	}
	if name == "/bin/kill" && len(args) > 0 {
		r.mu.Lock()
		r.signals = append(r.signals, strings.TrimPrefix(args[0], "-"))
		r.mu.Unlock()
	}
	return (macos.ExecRunner{}).CombinedOutput(ctx, name, args...)
}

func TestApplyLeavesBundlesUntouchedWhenSIGTERMFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, launched: true, quitRefused: true, termRefused: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Second, Runner: runner}

	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil)
	if err == nil || !strings.Contains(err.Error(), "User canceled") || !strings.Contains(err.Error(), "send SIGTERM") {
		t.Fatalf("expected both shutdown errors, got %v", err)
	}
	if build := readBuild(t, appPath); build != "1" {
		t.Fatalf("installed build %s after failed shutdown, want 1", build)
	}
	if build := readBuild(t, stagedPath); build != "2" {
		t.Fatalf("staged build %s after failed shutdown, want 2", build)
	}
}

func TestApplyRestoresOldBundleWhenReplacementDoesNotStart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, neverReady: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Nanosecond, Runner: runner}
	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil)
	if err == nil || !strings.Contains(err.Error(), "previous app restored") {
		t.Fatalf("expected restored-app error, got %v", err)
	}
	if build := readBuild(t, appPath); build != "1" {
		t.Fatalf("installed build %s after rollback, want 1", build)
	}
	marker := installer.failurePath("2")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("failure marker missing: %v", err)
	}
	failedBundles, err := filepath.Glob(filepath.Join(root, ".ChatGPT.app.codex-autoupdate-failed-*"))
	if err != nil || len(failedBundles) != 0 {
		t.Fatalf("failed replacement was not cleaned up: %v, %v", failedBundles, err)
	}
	if _, err := installer.Prepare(context.Background(), appcast.Release{Build: "2", Version: "2.0"}); err == nil || !strings.Contains(err.Error(), "quarantined") {
		t.Fatalf("expected build quarantine on retry, got %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, err := installer.Prepare(context.Background(), appcast.Release{Build: "2", Version: "2.0"}); err == nil || strings.Contains(err.Error(), "quarantined") {
		t.Fatalf("expected deliberate marker removal to permit retry, got %v", err)
	}
}

func TestRollbackTerminatesRunningFailedReplacementBeforeRestore(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, readinessBlocked: true, quitRefused: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Nanosecond, Runner: runner}

	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil)
	if err == nil || !strings.Contains(err.Error(), "previous app restored") {
		t.Fatalf("expected successful rollback report, got %v", err)
	}
	if build := readBuild(t, appPath); build != "1" {
		t.Fatalf("installed build %s after rollback, want 1", build)
	}
	runner.mu.Lock()
	commands := append([]string(nil), runner.commands...)
	runner.mu.Unlock()
	want := []string{"/usr/bin/open", "/usr/bin/osascript", "/bin/kill -TERM 123", "/usr/bin/open"}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("rollback command sequence %v, want %v", commands, want)
	}
	if _, err := os.Stat(installer.failurePath("2")); err != nil {
		t.Fatalf("rollback quarantine marker missing: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(root, ".ChatGPT.app.codex-autoupdate-backup-*"))
	if err != nil || len(backups) != 0 {
		t.Fatalf("rollback backup was not cleaned up: %v, %v", backups, err)
	}
}

func TestRollbackDoesNotMoveBundlesWhenFailedReplacementCannotStop(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, readinessBlocked: true, quitRefused: true, termRefused: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Nanosecond, Runner: runner}

	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, nil)
	if err == nil || !strings.Contains(err.Error(), "rollback could not stop failed replacement") {
		t.Fatalf("expected safe rollback refusal, got %v", err)
	}
	if build := readBuild(t, appPath); build != "2" {
		t.Fatalf("running replacement bundle moved despite failed shutdown; build = %s", build)
	}
	backups, globErr := filepath.Glob(filepath.Join(root, ".ChatGPT.app.codex-autoupdate-backup-*"))
	if globErr != nil || len(backups) != 1 || readBuild(t, backups[0]) != "1" {
		t.Fatalf("previous bundle was not retained for recovery: %v, %v", backups, globErr)
	}
	if _, statErr := os.Stat(installer.failurePath("2")); statErr != nil {
		t.Fatalf("failed replacement was not quarantined: %v", statErr)
	}
}

func TestApplyRelaunchesPreviousAppWhenActivationRenameFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, launched: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Second, Runner: runner}
	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: "2", Version: "2.0"}, StagedPath: stagedPath}, func(context.Context) error {
		return os.RemoveAll(stagedPath)
	})
	if err == nil || !strings.Contains(err.Error(), "previous app relaunched") {
		t.Fatalf("expected relaunch after activation failure, got %v", err)
	}
	if build := readBuild(t, appPath); build != "1" {
		t.Fatalf("installed build %s after failed activation, want 1", build)
	}
	runner.mu.Lock()
	launched := runner.launched
	runner.mu.Unlock()
	if !launched {
		t.Fatal("previous app was not relaunched")
	}
	if _, err := os.Stat(installer.failurePath("2")); err != nil {
		t.Fatalf("activation failure marker missing: %v", err)
	}
}

func TestClaudeDottedReleasePreparesAndApplies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "Claude.app")
	writeFakeBundleFor(t, appPath, "1.24011.0", "1.24011.0", macos.ClaudeIdentity)
	source := filepath.Join(root, "source", "Claude.app")
	writeFakeBundleFor(t, source, "1.24012.1", "1.24012.1", macos.ClaudeIdentity)
	archivePath := filepath.Join(root, "Claude.zip")
	if output, err := exec.Command("/usr/bin/ditto", "-c", "-k", "--keepParent", source, archivePath).CombinedOutput(); err != nil {
		t.Fatalf("create fixture archive: %v: %s", err, output)
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(archive)
	}))
	defer server.Close()
	runner := &fixtureRunner{appPath: appPath, identity: macos.ClaudeIdentity}
	installer := Installer{
		AppPath:       appPath,
		CacheDir:      filepath.Join(root, "cache", "claude"),
		QuitTimeout:   time.Second,
		LaunchTimeout: time.Second,
		HTTPClient:    server.Client(),
		Runner:        runner,
		Identity:      macos.ClaudeIdentity,
	}
	candidate := appcast.Release{Build: "1.24012.1", Version: "1.24012.1", URL: server.URL, Length: int64(len(archive))}
	prepared, err := installer.Prepare(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := installer.Apply(context.Background(), prepared, nil); err != nil {
		t.Fatal(err)
	}
	if build := readBuild(t, appPath); build != "1.24012.1" {
		t.Fatalf("installed build %s, want 1.24012.1", build)
	}
}

func TestFindExtractedAppRejectsSymlinkBundle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "Claude.app")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "Claude.app")); err != nil {
		t.Fatal(err)
	}
	if _, err := findExtractedApp(root, "Claude.app"); err == nil {
		t.Fatal("expected symlink bundle rejection")
	}
}

func TestPrepareHonorsLegacyChatGPTQuarantineMarker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	writeFakeBundle(t, appPath, "2.0", 2)
	cacheRoot := filepath.Join(root, "cache")
	cacheDir := filepath.Join(cacheRoot, "chatgpt")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyMarker := filepath.Join(cacheRoot, "failed-build-2.json")
	if err := os.WriteFile(legacyMarker, []byte(`{"build":2,"error":"legacy failure"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := Installer{
		AppPath:       appPath,
		CacheDir:      cacheDir,
		QuitTimeout:   time.Second,
		LaunchTimeout: time.Second,
	}
	_, err := installer.Prepare(context.Background(), appcast.Release{Build: "2"})
	if err == nil || !strings.Contains(err.Error(), legacyMarker) {
		t.Fatalf("expected legacy quarantine marker, got %v", err)
	}
}

type fixtureRunner struct {
	appPath          string
	identity         macos.Identity
	neverReady       bool
	readinessBlocked bool
	quitRefused      bool
	quitIgnored      bool
	termRefused      bool
	helpers          map[int]bool
	respawnOnPSCall  int

	mu       sync.Mutex
	launched bool
	commands []string
	psCalls  int
}

func (r *fixtureRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "/usr/libexec/PlistBuddy", "/usr/bin/ditto":
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	case "/usr/bin/codesign":
		r.mu.Lock()
		readinessBlocked := r.readinessBlocked
		launched := r.launched
		r.mu.Unlock()
		if readinessBlocked && launched && len(args) > 0 && args[len(args)-1] == r.appPath && readBuildValue(r.appPath) == "2" {
			return []byte("replacement readiness blocked"), fmt.Errorf("exit status 1")
		}
		if len(args) > 0 && args[0] == "-dv" {
			identity := r.bundleIdentity()
			return []byte("Identifier=" + identity.BundleIdentifier + "\nTeamIdentifier=" + identity.TeamID + "\n"), nil
		}
		return nil, nil
	case "/usr/sbin/spctl":
		return []byte("accepted"), nil
	case "/usr/bin/lipo":
		architecture := runtime.GOARCH
		if architecture == "amd64" {
			architecture = "x86_64"
		}
		return []byte(architecture), nil
	case "/usr/bin/open":
		r.mu.Lock()
		r.launched = true
		r.commands = append(r.commands, name)
		r.mu.Unlock()
		return nil, nil
	case "/usr/bin/osascript":
		r.mu.Lock()
		r.commands = append(r.commands, name)
		if r.quitRefused {
			r.mu.Unlock()
			return []byte("execution error: User canceled. (-128)"), fmt.Errorf("exit status 1")
		}
		if !r.quitIgnored {
			r.launched = false
		}
		r.mu.Unlock()
		return nil, nil
	case "/bin/kill":
		r.mu.Lock()
		r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
		if r.termRefused {
			r.mu.Unlock()
			return []byte("Operation not permitted"), fmt.Errorf("exit status 1")
		}
		signal := args[0]
		for _, value := range args[1:] {
			pid, err := strconv.Atoi(value)
			if err != nil {
				continue
			}
			if pid == 123 {
				r.launched = false
			}
			ignoresTERM, helper := r.helpers[pid]
			if helper && (signal == "-KILL" || !ignoresTERM) {
				delete(r.helpers, pid)
			}
		}
		r.mu.Unlock()
		return nil, nil
	case "/bin/ps":
		r.mu.Lock()
		r.psCalls++
		if r.respawnOnPSCall == r.psCalls {
			r.launched = true
		}
		launched := r.launched
		helpers := make(map[int]bool, len(r.helpers))
		maps.Copy(helpers, r.helpers)
		r.mu.Unlock()
		var output string
		if _, ok := helpers[122]; ok {
			output += fmt.Sprintf("122 Fri Jul 17 09:00:00 2026 %s/Contents/Helpers/helper-122\n", r.appPath)
		}
		if launched && !r.neverReady {
			output += fmt.Sprintf("123 Fri Jul 17 09:30:03 2026 %s/Contents/MacOS/%s\n", r.appPath, r.bundleIdentity().Executable)
		}
		for _, pid := range []int{124, 125} {
			if _, ok := helpers[pid]; ok {
				output += fmt.Sprintf("%d Fri Jul 17 09:00:00 2026 %s/Contents/Helpers/helper-%d\n", pid, r.appPath, pid)
			}
		}
		return []byte(output), nil
	default:
		return nil, fmt.Errorf("unexpected command %s %v", name, args)
	}
}

func (r *fixtureRunner) bundleIdentity() macos.Identity {
	if r.identity.BundleIdentifier == "" {
		return macos.ChatGPTIdentity
	}
	return r.identity
}

func readBuildValue(appPath string) string {
	output, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleVersion", filepath.Join(appPath, "Contents", "Info.plist")).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func writeFakeBundle(t *testing.T, path, version string, build int) {
	t.Helper()
	writeFakeBundleFor(t, path, version, fmt.Sprint(build), macos.ChatGPTIdentity)
}

func writeFakeBundleFor(t *testing.T, path, version, build string, identity macos.Identity) {
	t.Helper()
	contents := filepath.Join(path, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>%s</string>
<key>CFBundleShortVersionString</key><string>%s</string>
<key>CFBundleVersion</key><string>%s</string>
</dict></plist>`, identity.BundleIdentifier, version, build)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "MacOS", identity.Executable), []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readBuild(t *testing.T, appPath string) string {
	t.Helper()
	output, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleVersion", filepath.Join(appPath, "Contents", "Info.plist")).CombinedOutput()
	if err != nil {
		t.Fatalf("read fixture build: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}
