package watch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/activity"
	"github.com/tylergannon/codex-autoupdate/internal/appcast"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
	"github.com/tylergannon/codex-autoupdate/internal/update"
)

type fakeFeed struct{ release appcast.Release }

func (f fakeFeed) Latest(context.Context) (appcast.Release, error) { return f.release, nil }

type fakeInspector struct{ build int64 }

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
}

func (i *fakeInstaller) Prepare(context.Context, appcast.Release) (update.Prepared, error) {
	i.prepared = true
	return update.Prepared{}, nil
}

func (i *fakeInstaller) Apply(ctx context.Context, _ update.Prepared, preflight func(context.Context) error) error {
	if err := preflight(ctx); err != nil {
		return err
	}
	i.applied = true
	i.inspector.build = 2
	return nil
}

func TestWatcherWaitsForContinuousIdleAndRechecksBeforeApply(t *testing.T) {
	t.Parallel()
	clock := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	inspector := &fakeInspector{build: 1}
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
		Feed:                 fakeFeed{appcast.Release{Build: 2}},
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

func TestWatcherDoesNothingWhenCurrent(t *testing.T) {
	t.Parallel()
	inspector := &fakeInspector{build: 2}
	installer := &fakeInstaller{inspector: inspector}
	watcher := Watcher{
		AppPath:              "/Applications/ChatGPT.app",
		IdleWindow:           time.Minute,
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Second,
		Feed:                 fakeFeed{appcast.Release{Build: 2}},
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
	inspector := &fakeInspector{build: 1}
	installer := &fakeInstaller{inspector: inspector}
	watcher := Watcher{
		AppPath:              "/Applications/ChatGPT.app",
		IdleWindow:           time.Minute,
		PollInterval:         time.Hour,
		ActivityPollInterval: time.Second,
		Feed:                 fakeFeed{appcast.Release{Build: 2}},
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
