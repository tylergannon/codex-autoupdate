package watch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/activity"
	"github.com/tylergannon/codex-autoupdate/internal/appcast"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
	"github.com/tylergannon/codex-autoupdate/internal/update"
)

type Feed interface {
	Latest(ctx context.Context) (appcast.Release, error)
}

type Activity interface {
	Detect(ctx context.Context) (activity.Report, error)
}

type BundleInspector interface {
	Inspect(ctx context.Context, appPath string, verify bool) (macos.Bundle, error)
}

type Installer interface {
	Prepare(ctx context.Context, release appcast.Release) (update.Prepared, error)
	Apply(ctx context.Context, prepared update.Prepared, preflight func(context.Context) error) error
}

type Watcher struct {
	AppPath              string
	IdleWindow           time.Duration
	PollInterval         time.Duration
	ActivityPollInterval time.Duration
	Feed                 Feed
	Activity             Activity
	Inspector            BundleInspector
	Installer            Installer
	Logger               *slog.Logger
	Now                  func() time.Time
	Sleep                func(context.Context, time.Duration) error
}

func (w Watcher) Run(ctx context.Context, once bool) error {
	if err := w.validate(); err != nil {
		return err
	}
	for {
		updated, err := w.cycle(ctx)
		if once {
			return err
		}
		if err != nil {
			w.logger().Error("watch cycle failed", "error", err)
		} else if updated {
			w.logger().Info("watch cycle installed an update")
		}
		if err := w.sleep(ctx, w.PollInterval); err != nil {
			return err
		}
	}
}

func (w Watcher) cycle(ctx context.Context) (bool, error) {
	installed, err := w.Inspector.Inspect(ctx, w.AppPath, false)
	if err != nil {
		return false, fmt.Errorf("inspect installed ChatGPT Desktop: %w", err)
	}
	release, err := w.Feed.Latest(ctx)
	if err != nil {
		return false, err
	}
	if release.Build <= installed.Build {
		w.logger().Info("ChatGPT Desktop is current", "installed_build", installed.Build, "available_build", release.Build)
		return false, nil
	}
	w.logger().Info("ChatGPT Desktop update available", "installed_build", installed.Build, "available_build", release.Build, "version", release.Version)
	prepared, err := w.Installer.Prepare(ctx, release)
	if err != nil {
		return false, err
	}
	if err := w.waitForIdle(ctx); err != nil {
		return false, err
	}
	installed, err = w.Inspector.Inspect(ctx, w.AppPath, false)
	if err != nil {
		return false, fmt.Errorf("reinspect installed ChatGPT Desktop: %w", err)
	}
	if installed.Build >= release.Build {
		w.logger().Info("ChatGPT Desktop updated independently while watcher waited", "installed_build", installed.Build)
		return false, nil
	}
	preflight := func(ctx context.Context) error {
		report, err := w.Activity.Detect(ctx)
		if err != nil {
			return err
		}
		if report.Active() {
			return fmt.Errorf("desktop task became active: %v", report.ActiveThreads)
		}
		if !report.LastLifecycle.IsZero() && w.now().Sub(report.LastLifecycle) < w.IdleWindow {
			return fmt.Errorf("desktop idle window restarted at %s", report.LastLifecycle.Format(time.RFC3339))
		}
		return nil
	}
	if err := w.Installer.Apply(ctx, prepared, preflight); err != nil {
		return false, err
	}
	return true, nil
}

func (w Watcher) waitForIdle(ctx context.Context) error {
	lastLoggedActive := ""
	for {
		report, err := w.Activity.Detect(ctx)
		if err != nil {
			return fmt.Errorf("inspect Desktop task activity: %w", err)
		}
		if report.Active() {
			active := fmt.Sprint(report.ActiveThreads)
			if active != lastLoggedActive {
				w.logger().Info("waiting for Desktop tasks to finish", "threads", report.ActiveThreads)
				lastLoggedActive = active
			}
		} else {
			idleSince := report.LastLifecycle
			if idleSince.IsZero() {
				w.logger().Info("Desktop app-server is not running; no local tasks can be active")
				return nil
			}
			remaining := w.IdleWindow - w.now().Sub(idleSince)
			if remaining <= 0 {
				w.logger().Info("Desktop idle window satisfied", "idle_since", idleSince.Format(time.RFC3339), "idle_window", w.IdleWindow)
				return nil
			}
			if lastLoggedActive != "idle" {
				w.logger().Info("waiting for uninterrupted Desktop idle window", "idle_since", idleSince.Format(time.RFC3339), "remaining", remaining.Round(time.Second))
				lastLoggedActive = "idle"
			}
		}
		if err := w.sleep(ctx, w.ActivityPollInterval); err != nil {
			return err
		}
	}
}

func (w Watcher) validate() error {
	if w.Feed == nil || w.Activity == nil || w.Inspector == nil || w.Installer == nil {
		return fmt.Errorf("watcher dependencies are incomplete")
	}
	if w.IdleWindow <= 0 || w.PollInterval <= 0 || w.ActivityPollInterval <= 0 {
		return fmt.Errorf("watch intervals must be positive")
	}
	return nil
}

func (w Watcher) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

func (w Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w Watcher) sleep(ctx context.Context, duration time.Duration) error {
	if w.Sleep != nil {
		return w.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
