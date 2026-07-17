package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/appcast"
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
	installer := Installer{
		AppPath:       appPath,
		CacheDir:      filepath.Join(root, "cache"),
		QuitTimeout:   time.Second,
		LaunchTimeout: time.Second,
		HTTPClient:    server.Client(),
		Runner:        runner,
	}
	release := appcast.Release{Build: 2, Version: "2.0", URL: server.URL, Length: int64(len(archive))}
	prepared, err := installer.Prepare(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.StagedPath != installer.stagedPath(2) {
		t.Fatalf("unexpected staged path: %s", prepared.StagedPath)
	}
	if build := readBuild(t, prepared.StagedPath); build != "2" {
		t.Fatalf("staged build %s, want 2", build)
	}
}

func TestApplyAtomicallyReplacesAndWaitsForAppServer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Second, Runner: runner}
	prepared := Prepared{Release: appcast.Release{Build: 2, Version: "2.0"}, StagedPath: stagedPath}
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

func TestApplyRestoresOldBundleWhenReplacementDoesNotStart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	appPath := filepath.Join(root, "ChatGPT.app")
	stagedPath := filepath.Join(root, ".ChatGPT.app.codex-autoupdate-2.new")
	writeFakeBundle(t, appPath, "1.0", 1)
	writeFakeBundle(t, stagedPath, "2.0", 2)
	runner := &fixtureRunner{appPath: appPath, neverReady: true}
	installer := Installer{AppPath: appPath, CacheDir: filepath.Join(root, "cache"), QuitTimeout: time.Second, LaunchTimeout: time.Nanosecond, Runner: runner}
	err := installer.Apply(context.Background(), Prepared{Release: appcast.Release{Build: 2, Version: "2.0"}, StagedPath: stagedPath}, nil)
	if err == nil || !strings.Contains(err.Error(), "previous app restored") {
		t.Fatalf("expected restored-app error, got %v", err)
	}
	if build := readBuild(t, appPath); build != "1" {
		t.Fatalf("installed build %s after rollback, want 1", build)
	}
}

type fixtureRunner struct {
	appPath    string
	neverReady bool

	mu       sync.Mutex
	launched bool
}

func (r *fixtureRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	switch name {
	case "/usr/libexec/PlistBuddy", "/usr/bin/ditto":
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	case "/usr/bin/codesign":
		if len(args) > 0 && args[0] == "-dv" {
			return []byte("Identifier=com.openai.codex\nTeamIdentifier=2DC432GLL2\n"), nil
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
		r.mu.Unlock()
		return nil, nil
	case "/usr/bin/osascript":
		r.mu.Lock()
		r.launched = false
		r.mu.Unlock()
		return nil, nil
	case "/bin/ps":
		r.mu.Lock()
		launched := r.launched
		r.mu.Unlock()
		if launched && !r.neverReady {
			return []byte(fmt.Sprintf("123 Fri Jul 17 09:30:03 2026 %s/Contents/Resources/codex app-server\n", r.appPath)), nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected command %s %v", name, args)
	}
}

func writeFakeBundle(t *testing.T, path, version string, build int) {
	t.Helper()
	contents := filepath.Join(path, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.openai.codex</string>
<key>CFBundleShortVersionString</key><string>%s</string>
<key>CFBundleVersion</key><string>%d</string>
</dict></plist>`, version, build)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "MacOS", "ChatGPT"), []byte("fixture"), 0o755); err != nil {
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
