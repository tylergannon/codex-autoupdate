package activity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/macos"
)

type staticProcessSource struct {
	process *macos.Process
}

func (s staticProcessSource) DesktopAppServer(context.Context, string) (*macos.Process, error) {
	return s.process, nil
}

func TestDetectorReportsOnlyActiveDesktopRolloutsSinceServerStart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	serverStart := time.Now().Add(-time.Hour).Truncate(time.Second)
	activeStart := serverStart.Add(10 * time.Minute)
	writeRollout(t, home, "active", "Codex Desktop", []lifecycle{{"task_started", activeStart}}, false)
	writeRollout(t, home, "complete", "Codex Desktop", []lifecycle{{"task_started", activeStart}, {"task_complete", activeStart.Add(time.Minute)}}, false)
	writeRollout(t, home, "aborted", "Codex Desktop", []lifecycle{{"task_started", activeStart}, {"turn_aborted", activeStart.Add(2 * time.Minute)}}, false)
	writeRollout(t, home, "cli", "Codex CLI", []lifecycle{{"task_started", activeStart.Add(3 * time.Minute)}}, false)
	stale := writeRollout(t, home, "stale", "Codex Desktop", []lifecycle{{"task_started", serverStart.Add(-time.Hour)}}, false)
	old := serverStart.Add(-time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	writeRollout(t, home, "partial", "Codex Desktop", []lifecycle{{"task_started", activeStart.Add(4 * time.Minute)}, {"task_complete", activeStart.Add(5 * time.Minute)}}, true)
	corrupt := writeRollout(t, home, "corrupt", "Codex Desktop", []lifecycle{{"task_started", activeStart}, {"task_complete", activeStart.Add(time.Minute)}}, false)
	file, err := os.OpenFile(corrupt, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("not-json\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	corruptActive := writeRollout(t, home, "corrupt-active", "Codex Desktop", []lifecycle{{"task_started", activeStart.Add(6 * time.Minute)}}, false)
	file, err = os.OpenFile(corruptActive, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("not-json\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	detector := Detector{
		AppPath:   "/Applications/ChatGPT.app",
		CodexHome: home,
		Processes: staticProcessSource{&macos.Process{PID: 42, Started: serverStart}},
	}
	report, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(report.ActiveThreads, []string{"active", "corrupt-active"}) {
		t.Fatalf("unexpected active threads: %v", report.ActiveThreads)
	}
	if !report.LastLifecycle.Equal(activeStart.Add(6 * time.Minute)) {
		t.Fatalf("unexpected last lifecycle: %s", report.LastLifecycle)
	}
	if len(report.Warnings) != 2 || !strings.Contains(strings.Join(report.Warnings, "\n"), "corrupt-active") {
		t.Fatalf("unexpected warnings: %v", report.Warnings)
	}
}

func TestDetectorTreatsMissingDesktopServerAsIdle(t *testing.T) {
	t.Parallel()
	detector := Detector{Processes: staticProcessSource{}}
	report, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Active() || !report.LastLifecycle.IsZero() {
		t.Fatalf("unexpected report: %+v", report)
	}
}

type lifecycle struct {
	event string
	at    time.Time
}

func writeRollout(t *testing.T, home, id, originator string, events []lifecycle, partial bool) string {
	t.Helper()
	directory := filepath.Join(home, "sessions", "2026", "07", "17")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, id+".jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintf(file, "{\"timestamp\":\"%s\",\"type\":\"session_meta\",\"payload\":{\"id\":\"%s\",\"originator\":\"%s\"}}\n", events[0].at.Format(time.RFC3339Nano), id, originator)
	for _, event := range events {
		_, _ = fmt.Fprintf(file, "{\"timestamp\":\"%s\",\"type\":\"event_msg\",\"payload\":{\"type\":\"%s\"}}\n", event.at.Format(time.RFC3339Nano), event.event)
	}
	_, _ = file.WriteString("\n")
	if partial {
		_, _ = file.WriteString(`{"timestamp":"unterminated`)
	}
	return path
}
