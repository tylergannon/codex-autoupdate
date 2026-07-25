package watch

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	inspector *fakeInspector
	prepared  bool
	applied   bool
	applyErr  error
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
