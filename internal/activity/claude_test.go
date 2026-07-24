package activity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/macos"
)

type claudeProcesses struct {
	application *macos.Process
	processes   []macos.Process
}

func (p claudeProcesses) All(context.Context) ([]macos.Process, error) {
	return p.processes, nil
}

func (p claudeProcesses) Application(context.Context, string, string) (*macos.Process, error) {
	return p.application, nil
}

func TestClaudeDetectorOnlyReportsLiveSessionWorkers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	started := time.Date(2026, 7, 24, 10, 0, 0, 0, time.Local)
	writeClaudeSession(t, root, "live", "worker-live", false, started.Add(time.Minute))
	writeClaudeSession(t, root, "dormant", "worker-dormant", false, started.Add(2*time.Minute))
	writeClaudeSession(t, root, "archived", "worker-archived", true, started.Add(3*time.Minute))
	detector := ClaudeDetector{
		AppPath:    "/Applications/Claude.app",
		ClaudeData: root,
		Processes: claudeProcesses{
			application: &macos.Process{PID: 10, Started: started},
			processes: []macos.Process{
				{PID: 10, Command: "/Applications/Claude.app/Contents/MacOS/Claude"},
				{PID: 11, Command: "/bin/claude --session worker-live"},
				{PID: 12, Command: "/bin/claude --session worker-archived"},
			},
		},
	}
	report, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(report.ActiveThreads) != "[live]" {
		t.Fatalf("active sessions = %v, want [live]", report.ActiveThreads)
	}
	if !report.LastLifecycle.Equal(started.Add(2 * time.Minute)) {
		t.Fatalf("last lifecycle = %s", report.LastLifecycle)
	}
}

func TestClaudeDetectorTreatsClosedApplicationAsIdle(t *testing.T) {
	t.Parallel()
	report, err := (ClaudeDetector{
		AppPath:    "/Applications/Claude.app",
		ClaudeData: t.TempDir(),
		Processes:  claudeProcesses{},
	}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Active() || !report.LastLifecycle.IsZero() {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestClaudeDetectorRejectsUnreadableSessionState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "claude-code-sessions", "account", "org", "local_bad.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Hour)
	_, err := (ClaudeDetector{
		AppPath:    "/Applications/Claude.app",
		ClaudeData: root,
		Processes:  claudeProcesses{application: &macos.Process{PID: 10, Started: started}},
	}).Detect(context.Background())
	if err == nil {
		t.Fatal("expected corrupt activity state error")
	}
}

func writeClaudeSession(t *testing.T, root, sessionID, processName string, archived bool, lastActivity time.Time) {
	t.Helper()
	path := filepath.Join(root, "claude-code-sessions", "account", "org", "local_"+sessionID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`{"sessionId":%q,"processName":%q,"isArchived":%t,"lastActivityAt":%d}`,
		sessionID, processName, archived, lastActivity.UnixMilli())
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
