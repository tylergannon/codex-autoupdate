package launchagent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRenderPlistPreservesArgumentsAndEscapesXML(t *testing.T) {
	t.Parallel()
	content, err := renderPlist(Config{
		Executable:           "/tmp/a&b/codex-autoupdate",
		AppPath:              "/Applications/ChatGPT.app",
		CodexHome:            "/tmp/.codex",
		CacheDir:             "/tmp/cache",
		FeedURL:              "https://example.test/appcast.xml?a=1&b=2",
		IdleWindow:           "5m0s",
		PollInterval:         "15m0s",
		ActivityPollInterval: "5s",
		QuitTimeout:          "30s",
		LaunchTimeout:        "1m30s",
	}, "/tmp/out.log", "/tmp/err.log")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{"/tmp/a&amp;b/codex-autoupdate", "appcast.xml?a=1&amp;b=2", "--idle-window", "5m0s", "<key>KeepAlive</key>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("plist missing %q:\n%s", expected, text)
		}
	}
}

func TestManagerInstallRefreshStatusAndUninstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	runner := &recordingRunner{status: "service = running\n"}
	manager := Manager{
		HomeDir:      home,
		UID:          501,
		Runner:       runner,
		RequireAdmin: func() error { return nil },
	}
	paths, err := manager.paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.binary, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(home, "Applications", "ChatGPT.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	config := testConfig(paths.binary, appPath)
	config.IdleWindow = "10m0s"
	config.PollInterval = "30m0s"

	if err := manager.Install(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	plist, err := os.ReadFile(paths.plist)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{paths.binary, "10m0s", "30m0s", appPath} {
		if !strings.Contains(string(plist), expected) {
			t.Fatalf("plist missing %q:\n%s", expected, plist)
		}
	}
	wantCommands := []string{
		"/bin/launchctl bootout gui/501/" + Label,
		"/bin/launchctl bootstrap gui/501 " + paths.plist,
		"/bin/launchctl kickstart gui/501/" + Label,
	}
	if !slices.Equal(runner.commands, wantCommands) {
		t.Fatalf("commands = %v, want %v", runner.commands, wantCommands)
	}
	if err := manager.Install(context.Background(), config); err != nil {
		t.Fatalf("refresh install: %v", err)
	}
	wantCommands = append(wantCommands, wantCommands...)
	if !slices.Equal(runner.commands, wantCommands) {
		t.Fatalf("refresh commands = %v, want %v", runner.commands, wantCommands)
	}

	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status != runner.status {
		t.Fatalf("status = %q, want %q", status, runner.status)
	}
	logMarker := filepath.Join(filepath.Dir(paths.stdout), "keep.log")
	if err := os.WriteFile(logMarker, []byte("diagnostic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Uninstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{paths.binary, paths.plist} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, got %v", removed, err)
		}
	}
	if _, err := os.Stat(logMarker); err != nil {
		t.Fatalf("diagnostic log was not retained: %v", err)
	}
}

func TestManagerInstallRejectsNoncanonicalExecutableAndMissingApp(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	runner := &recordingRunner{}
	manager := Manager{HomeDir: home, UID: 501, Runner: runner, RequireAdmin: func() error { return nil }}
	paths, err := manager.paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.binary, []byte("canonical"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(home, "other", "codex-autoupdate")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(home, "ChatGPT.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}

	err = manager.Install(context.Background(), testConfig(other, appPath))
	if err == nil || !strings.Contains(err.Error(), "LaunchAgent requires") {
		t.Fatalf("expected canonical-path error, got %v", err)
	}
	err = manager.Install(context.Background(), testConfig(paths.binary, filepath.Join(home, "missing", "ChatGPT.app")))
	if err == nil || !strings.Contains(err.Error(), "ChatGPT Desktop is not installed") {
		t.Fatalf("expected missing-app error, got %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("launchctl ran on rejected install: %v", runner.commands)
	}
}

type recordingRunner struct {
	commands []string
	status   string
}

func (r *recordingRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	if slices.Contains(args, "print") {
		return []byte(r.status), nil
	}
	return nil, nil
}

func testConfig(executable, appPath string) Config {
	return Config{
		Executable:           executable,
		AppPath:              appPath,
		CodexHome:            filepath.Join(filepath.Dir(appPath), ".codex"),
		CacheDir:             filepath.Join(filepath.Dir(appPath), "cache"),
		FeedURL:              "https://example.test/appcast.xml",
		IdleWindow:           "5m0s",
		PollInterval:         "15m0s",
		ActivityPollInterval: "5s",
		QuitTimeout:          "30s",
		LaunchTimeout:        "1m30s",
	}
}

var _ Runner = (*recordingRunner)(nil)
