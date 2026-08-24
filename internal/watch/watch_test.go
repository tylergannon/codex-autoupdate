package watch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/activity"
	"github.com/tylergannon/codex-autoupdate/internal/appcast"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
	"github.com/tylergannon/codex-autoupdate/internal/update"
)

type fakeFeed struct {
	release appcast.Release
	err     error
}

func (f fakeFeed) Latest(context.Context) (appcast.Release, error) { return f.release, f.err }

type fakeInspector struct{ build string }

func (f *fakeInspector) Inspect(context.Context, string, bool) (macos.Bundle, error) {
	return macos.Bundle{Build: f.build}, nil
}

type sequenceActivity struct {
	reports []activity.Report
	index   int
}

func (a *sequenceActivity) Detect(context.Context) (activity.Report, error) {
	if a.index >= len(a.reports) {
		return a.reports[len(a.reports)-1], nil
	}
	report := a.reports[a.index]
	a.index++
	return report, nil
}

type fakeInstaller struct {
	inspector  *fakeInspector
	prepared   bool
	applied    bool
	applyCount int
	applyErr   error
}

func (i *fakeInstaller) Prepare(context.Context, appcast.Release) (update.Prepared, error) {
	i.prepared = true
	return update.Prepared{}, nil
}

func (i *fakeInstaller) Apply(ctx context.Context, _ update.Prepared, preflight func(context.Context) error) error {
	if err := preflight(ctx); err != nil {
		return err
	}
	if i.applyErr != nil {
		return i.applyErr
	}
	i.applied = true
	i.applyCount++
	i.inspector.build = "2"
	return nil
}

func TestWatcherForceReinstallsEqualVersion(t *testing.T) {
	t.Parallel()
	inspector := &fakeInspector{build: "2"}
	installer := &fakeInstaller{inspector: inspector}
	watcher := testWatcher(inspector, installer, fakeFeed{release: appcast.Release{Build: "2"}})
	state, err := watcher.Step(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if state != Updated || !installer.prepared || !installer.applied {
		t.Fatalf("state = %v, installer = %+v", state, installer)
	}
	state, err = watcher.Step(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if state != Updated || installer.applyCount != 2 {
		t.Fatalf("second state = %v, installer = %+v", state, installer)
	}
}

func TestWatcherForceNeverDowngrades(t *testing.T) {
	t.Parallel()
	inspector := &fakeInspector{build: "3"}
	installer := &fakeInstaller{inspector: inspector}
	watcher := testWatcher(inspector, installer, fakeFeed{release: appcast.Release{Build: "2"}})
	state, err := watcher.Step(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if state != Done || installer.prepared || installer.applied {
		t.Fatalf("state = %v, installer = %+v", state, installer)
	}
}

func TestWatcherForceAbandonsStagedWorkAfterIndependentUpdate(t *testing.T) {
	t.Parallel()
	inspector := &fakeInspector{build: "1"}
	installer := &fakeInstaller{inspector: inspector}
	watcher := testWatcher(inspector, installer, fakeFeed{release: appcast.Release{Build: "2"}})
	watcher.Activity = &sequenceActivity{reports: []activity.Report{
		{ActiveThreads: []string{"busy"}},
		{},
	}}
	state, err := watcher.Step(context.Background(), true)
	if err != nil || state != Pending || !installer.prepared {
		t.Fatalf("initial state = %v, error = %v, installer = %+v", state, err, installer)
	}
	inspector.build = "2"
	state, err = watcher.Step(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if state != Done || installer.applied {
		t.Fatalf("state after independent update = %v, installer = %+v", state, installer)
	}
}

// TestWatcherRemovesStagedResidueWhenApplicationBecomesCurrentIndependently is
// a regression test for the established bug recorded in
// ephemeral/proof/bug-adjudication.md (bug B): live evidence on the host
// showed a full 801 MiB abandoned staged bundle,
// /Applications/.Claude.app.codex-autoupdate-1_34493_1.new, left behind after
// Claude updated itself to the staged build before the watcher applied it.
// Watcher.Step's "application is current" branch (internal/watch/watch.go)
// only calls w.clearPrepared(), which nils in-memory bookkeeping; it never
// asks the Installer to remove the on-disk staged bundle at
// w.prepared.StagedPath, and cleanupResidue is reachable only from a later
// Prepare() call for a non-current candidate. This test stages residue
// directly (bypassing Prepare, since the defect is in Step's bookkeeping, not
// in Prepare) and proves the residue file survives an "application is
// current" cycle.
func TestWatcherRemovesStagedResidueWhenApplicationBecomesCurrentIndependently(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stagedPath := filepath.Join(root, ".Claude.app.codex-autoupdate-2.new")
	if err := os.MkdirAll(stagedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagedPath, "marker"), []byte("staged bundle"), 0o644); err != nil {
		t.Fatal(err)
	}

	inspector := &fakeInspector{build: "2"} // installed build now equals the candidate: updated independently
	installer := &fakeInstaller{inspector: inspector}
	watcher := testWatcher(inspector, installer, fakeFeed{release: appcast.Release{Build: "2"}})
	watcher.prepared = &update.Prepared{Release: appcast.Release{Build: "2"}, StagedPath: stagedPath}
	watcher.preparedInstalledBuild = "1"

	state, err := watcher.Step(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if state != Done {
		t.Fatalf("state = %v, want Done", state)
	}
	if _, statErr := os.Stat(stagedPath); !os.IsNotExist(statErr) {
		t.Fatalf("abandoned staged bundle residue was not removed: %s (stat err = %v)", stagedPath, statErr)
	}
}

// TestWatcherDoesNotLogDuplicateStatusOnUnchangedPendingState is a regression
// test for the established bug recorded in ephemeral/proof/bug-adjudication.md
// (bug D): the live installed watcher's stderr.log had grown to 35,470 lines /
// 5,119,873 bytes after roughly four days, dominated by repeated, byte-for-byte
// identical "waiting for active work to finish" / "application is current"
// records emitted every ActivityPollInterval tick (as fast as ~5 seconds)
// regardless of whether the underlying report changed. Watcher.Step
// (internal/watch/watch.go) unconditionally logs on every call when work is
// active, with no gate on the reported state actually changing since the
// previous tick, so any sufficiently long pending or steady-state period grows
// the log file without bound purely from repetition. This test drives Step
// directly (no launchd, no timers) with an unchanged Active report across
// repeated ticks and counts identical log records.
func TestWatcherDoesNotLogDuplicateStatusOnUnchangedPendingState(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	inspector := &fakeInspector{build: "1"}
	installer := &fakeInstaller{inspector: inspector}
	watcher := testWatcher(inspector, installer, fakeFeed{release: appcast.Release{Build: "2"}})
	watcher.Logger = slog.New(slog.NewTextHandler(&buffer, nil))
	watcher.Activity = &sequenceActivity{reports: []activity.Report{{ActiveThreads: []string{"claude-task-pid:1"}}}}

	const ticks = 5
	for range ticks {
		if _, err := watcher.Step(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	}
	occurrences := strings.Count(buffer.String(), "waiting for active work to finish")
	if occurrences != 1 {
		t.Fatalf("logged %d identical status records across %d ticks with unchanged state, want 1 (log must not grow without bound while nothing changes)", occurrences, ticks)
	}
}

func TestCoordinatorBusyHarnessDoesNotBlockReadyHarness(t *testing.T) {
	t.Parallel()
	firstInspector := &fakeInspector{build: "1"}
	firstInstaller := &fakeInstaller{inspector: firstInspector}
	first := testWatcher(firstInspector, firstInstaller, fakeFeed{release: appcast.Release{Build: "2"}})
	first.ID = "chatgpt"
	first.Activity = &sequenceActivity{reports: []activity.Report{{ActiveThreads: []string{"busy"}}}}

	secondInspector := &fakeInspector{build: "1"}
	secondInstaller := &fakeInstaller{inspector: secondInspector}
	second := testWatcher(secondInspector, secondInstaller, fakeFeed{release: appcast.Release{Build: "2"}})
	second.ID = "claude"

	err := (Coordinator{
		Watchers:             []*Watcher{first, second},
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Second,
		Sleep:                func(context.Context, time.Duration) error { return context.Canceled },
	}).Run(context.Background(), false, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation after first pass, got %v", err)
	}
	if firstInstaller.applied {
		t.Fatal("busy first harness was applied")
	}
	if !secondInstaller.applied {
		t.Fatal("ready second harness was blocked")
	}
}

func TestCoordinatorOneShotAttemptsOtherHarnessAfterFailure(t *testing.T) {
	t.Parallel()
	firstInspector := &fakeInspector{build: "1"}
	firstInstaller := &fakeInstaller{inspector: firstInspector, applyErr: errors.New("activation failed")}
	first := testWatcher(firstInspector, firstInstaller, fakeFeed{release: appcast.Release{Build: "2"}})
	first.ID = "chatgpt"

	secondInspector := &fakeInspector{build: "1"}
	secondInstaller := &fakeInstaller{inspector: secondInspector}
	second := testWatcher(secondInspector, secondInstaller, fakeFeed{release: appcast.Release{Build: "2"}})
	second.ID = "claude"

	err := (Coordinator{
		Watchers:             []*Watcher{first, second},
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Second,
	}).Run(context.Background(), true, false)
	if err == nil || !strings.Contains(err.Error(), "activation failed") {
		t.Fatalf("expected first harness error, got %v", err)
	}
	if !secondInstaller.applied {
		t.Fatal("second harness was not attempted")
	}
}

func testWatcher(inspector *fakeInspector, installer *fakeInstaller, feed fakeFeed) *Watcher {
	return &Watcher{
		ID:                   "test",
		Name:                 "Test",
		AppPath:              "/Applications/Test.app",
		IdleWindow:           time.Minute,
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Second,
		Feed:                 feed,
		Activity:             &sequenceActivity{reports: []activity.Report{{}}},
		Inspector:            inspector,
		Installer:            installer,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestWatcherWaitsForContinuousIdleAndRechecksBeforeApply(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	inspector := &fakeInspector{build: "1"}
	installer := &fakeInstaller{inspector: inspector}
	activitySequence := &sequenceActivity{reports: []activity.Report{
		{ActiveThreads: []string{"thread"}, LastLifecycle: clock},
		{LastLifecycle: clock.Add(time.Minute)},
		{LastLifecycle: clock.Add(time.Minute)},
		{LastLifecycle: clock.Add(time.Minute)},
	}}
	watcher := Watcher{
		AppPath:              "/Applications/ChatGPT.app",
		IdleWindow:           2 * time.Minute,
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Minute,
		Feed:                 fakeFeed{release: appcast.Release{Build: "2"}},
		Activity:             activitySequence,
		Inspector:            inspector,
		Installer:            installer,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:                  func() time.Time { return clock },
		Sleep: func(_ context.Context, duration time.Duration) error {
			clock = clock.Add(duration)
			return nil
		},
	}
	if err := watcher.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !installer.prepared || !installer.applied {
		t.Fatalf("unexpected installer state: %+v", installer)
	}
}

func TestWatcherStartsIdleWindowWhenProcessOnlyActivityDisappears(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	inspector := &fakeInspector{build: "1"}
	installer := &fakeInstaller{inspector: inspector}
	watcher := testWatcher(inspector, installer, fakeFeed{release: appcast.Release{Build: "2"}})
	watcher.IdleWindow = 2 * time.Minute
	watcher.ActivityPollInterval = time.Minute
	watcher.Activity = &sequenceActivity{reports: []activity.Report{
		{ActiveThreads: []string{"claude-code-pid:42"}},
		{},
		{},
		{},
	}}
	watcher.Now = func() time.Time { return clock }
	watcher.Sleep = func(_ context.Context, duration time.Duration) error {
		clock = clock.Add(duration)
		return nil
	}
	if err := watcher.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !installer.applied {
		t.Fatal("replacement was not applied")
	}
	want := time.Date(2026, 7, 25, 12, 3, 0, 0, time.UTC)
	if !clock.Equal(want) {
		t.Fatalf("replacement applied at %s, want %s after a fresh idle window", clock, want)
	}
}

func TestWatcherDoesNothingWhenCurrent(t *testing.T) {
	t.Parallel()
	inspector := &fakeInspector{build: "2"}
	installer := &fakeInstaller{inspector: inspector}
	watcher := Watcher{
		AppPath:              "/Applications/ChatGPT.app",
		IdleWindow:           time.Minute,
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Second,
		Feed:                 fakeFeed{release: appcast.Release{Build: "2"}},
		Activity:             &sequenceActivity{reports: []activity.Report{{}}},
		Inspector:            inspector,
		Installer:            installer,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := watcher.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if installer.prepared || installer.applied {
		t.Fatal("installer was called for a current build")
	}
}

func TestWatcherPropagatesCanceledWait(t *testing.T) {
	t.Parallel()
	inspector := &fakeInspector{build: "1"}
	installer := &fakeInstaller{inspector: inspector}
	watcher := Watcher{
		AppPath:              "/Applications/ChatGPT.app",
		IdleWindow:           time.Minute,
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Second,
		Feed:                 fakeFeed{release: appcast.Release{Build: "2"}},
		Activity:             &sequenceActivity{reports: []activity.Report{{ActiveThreads: []string{"thread"}}}},
		Inspector:            inspector,
		Installer:            installer,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sleep:                func(context.Context, time.Duration) error { return context.Canceled },
	}
	if err := watcher.Run(context.Background(), true); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
