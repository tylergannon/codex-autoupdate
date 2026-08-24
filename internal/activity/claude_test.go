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
	openFiles   map[int]struct{}
}

func (p claudeProcesses) All(context.Context) ([]macos.Process, error) {
	return p.processes, nil
}

func (p claudeProcesses) Application(context.Context, string, string) (*macos.Process, error) {
	return p.application, nil
}

func (p claudeProcesses) OpenFilesUnder(context.Context, string) (map[int]struct{}, error) {
	return p.openFiles, nil
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
				{PID: 11, Command: "/bin/session-worker --session worker-live"},
				{PID: 12, Command: "/bin/session-worker --session worker-archived"},
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

func TestClaudeDetectorReportsStandaloneClaudeCodeAndTaskProcesses(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-time.Minute)
	report, err := (ClaudeDetector{
		AppPath:    "/Applications/Claude.app",
		ClaudeData: t.TempDir(),
		TaskRoot:   t.TempDir(),
		Processes: claudeProcesses{
			processes: []macos.Process{
				{PID: 20, Started: started, Command: "/Users/test/.local/bin/claude --print task"},
				{PID: 21, Started: started.Add(time.Second), Command: "/bin/zsh -c npm test"},
			},
			openFiles: map[int]struct{}{21: {}},
		},
	}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(report.ActiveThreads); got != "[claude-code-pid:20 claude-task-pid:21]" {
		t.Fatalf("active processes = %s", got)
	}
}

func TestClaudeDetectorDoesNotTreatDesktopHelpersAsClaudeCode(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-time.Hour)
	report, err := (ClaudeDetector{
		AppPath:    "/Applications/Claude.app",
		ClaudeData: t.TempDir(),
		TaskRoot:   t.TempDir(),
		Processes: claudeProcesses{
			application: &macos.Process{PID: 10, Started: started},
			processes: []macos.Process{
				{PID: 10, Started: started, Command: "/Applications/Claude.app/Contents/MacOS/Claude"},
				{PID: 11, Started: started, Command: "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper --type=gpu-process"},
			},
		},
	}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Active() {
		t.Fatalf("desktop helpers reported active: %v", report.ActiveThreads)
	}
}

// TestClaudeDetectorRecognizesRemoteCliRuntime is a regression test for the
// established bug recorded in ephemeral/proof/bug-adjudication.md (bug A):
// a live `~/.claude/remote/ccd-cli/<version>` process, reproduced independently
// by two safety-audit reviewers on 2026-08-24 (PIDs 28558/80886, one granted
// mcp__computer-use), is invisible to both Claude activity routes. Its
// basename is a version string, not "claude"/"claude-code" and not under
// ".local/share/claude/versions/", so isClaudeCodeProcess rejects it; its
// stdio are anonymous pipes, not files under TaskRoot, so the stdio fallback
// also misses it. The watcher (internal/watch/watch.go) then treats this
// harness as idle and may replace/restart Claude Desktop underneath it.
func TestClaudeDetectorRecognizesRemoteCliRuntime(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-time.Minute)
	report, err := (ClaudeDetector{
		AppPath:    "/Applications/Claude.app",
		ClaudeData: t.TempDir(),
		TaskRoot:   t.TempDir(),
		Processes: claudeProcesses{
			processes: []macos.Process{
				{
					PID:     28558,
					Started: started,
					Command: "/Users/tyler/.claude/remote/ccd-cli/2.1.237 --resume=80d8f9bf-f2a4-4266-bafa-385097d794d3 --allowedTools mcp__computer-use,mcp__slack",
				},
			},
			// openFiles is intentionally empty: live lsof evidence showed fds
			// 0/1/2 for this process were anonymous PIPEs, not files under
			// /private/tmp/claude-<uid>, so the stdio fallback cannot see it either.
		},
	}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Active() {
		t.Fatalf("live remote Claude Code runtime was not reported active: %+v", report)
	}
}

func TestClaudeDetectorExcludesDesktopProcessesOutsideConfiguredAppPath(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-time.Hour)
	report, err := (ClaudeDetector{
		AppPath:    "/Applications/Proof/Claude.app",
		ClaudeData: t.TempDir(),
		TaskRoot:   t.TempDir(),
		Processes: claudeProcesses{
			processes: []macos.Process{
				{PID: 10, Started: started, Command: "/Applications/Claude.app/Contents/MacOS/Claude"},
				{PID: 11, Started: started, Command: "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper --type=gpu-process"},
				{PID: 12, Started: started, Command: "/Users/test/.local/bin/claude --print task"},
			},
		},
	}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(report.ActiveThreads); got != "[claude-code-pid:12]" {
		t.Fatalf("active processes = %s, want only standalone Claude Code", got)
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
