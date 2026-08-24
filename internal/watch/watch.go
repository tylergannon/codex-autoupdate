package watch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/activity"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
	"github.com/tylergannon/codex-autoupdate/internal/release"
	"github.com/tylergannon/codex-autoupdate/internal/update"
)

type Feed interface {
	Latest(ctx context.Context) (release.Release, error)
}

type Activity interface {
	Detect(ctx context.Context) (activity.Report, error)
}

type BundleInspector interface {
	Inspect(ctx context.Context, appPath string, verify bool) (macos.Bundle, error)
}

type Installer interface {
	Prepare(ctx context.Context, release release.Release) (update.Prepared, error)
	Apply(ctx context.Context, prepared update.Prepared, preflight func(context.Context) error) error
}

type State int

const (
	Done State = iota
	Pending
	Updated
)

type Watcher struct {
	ID                     string
	Name                   string
	SetupError             error
	AppPath                string
	IdleWindow             time.Duration
	PollInterval           time.Duration
	ActivityPollInterval   time.Duration
	Feed                   Feed
	Activity               Activity
	Inspector              BundleInspector
	Installer              Installer
	Logger                 *slog.Logger
	Now                    func() time.Time
	Sleep                  func(context.Context, time.Duration) error
	prepared               *update.Prepared
	preparedInstalledBuild string
	observedActive         bool
	idleSince              time.Time
	lastActiveWork         string
	lastCurrentBuilds      string
}

func (w *Watcher) Run(ctx context.Context, once bool) error {
	coordinator := Coordinator{
		Watchers:             []*Watcher{w},
		PollInterval:         w.PollInterval,
		ActivityPollInterval: w.ActivityPollInterval,
		Logger:               w.Logger,
		Sleep:                w.Sleep,
	}
	return coordinator.Run(ctx, once, false)
}

func (w *Watcher) Step(ctx context.Context, force bool) (State, error) {
	if w.SetupError != nil {
		return Done, w.SetupError
	}
	if err := w.validate(); err != nil {
		return Done, err
	}
	installed, err := w.Inspector.Inspect(ctx, w.AppPath, false)
	if err != nil {
		return Done, fmt.Errorf("inspect installed %s: %w", w.name(), err)
	}
	candidate, err := w.Feed.Latest(ctx)
	if err != nil {
		return Done, err
	}
	comparison, err := release.Compare(candidate.Build, installed.Build)
	if err != nil {
		return Done, fmt.Errorf("compare %s versions: %w", w.name(), err)
	}
	if comparison < 0 {
		w.lastCurrentBuilds = ""
		if err := w.removePrepared(); err != nil {
			return Done, err
		}
		w.logger().Warn("available release is older than installed application; refusing downgrade", "harness", w.id(), "installed_build", installed.Build, "available_build", candidate.Build)
		return Done, nil
	}
	if comparison == 0 && !force {
		if err := w.removePrepared(); err != nil {
			return Done, err
		}
		builds := installed.Build + "\x00" + candidate.Build
		if builds != w.lastCurrentBuilds {
			w.logger().Info("application is current", "harness", w.id(), "installed_build", installed.Build, "available_build", candidate.Build)
			w.lastCurrentBuilds = builds
		}
		return Done, nil
	}
	w.lastCurrentBuilds = ""
	if w.prepared != nil && installed.Build != w.preparedInstalledBuild {
		w.logger().Info("installed application changed while replacement was staged; abandoning stale work", "harness", w.id(), "previous_build", w.preparedInstalledBuild, "installed_build", installed.Build)
		if err := w.removePrepared(); err != nil {
			return Done, err
		}
		if comparison <= 0 {
			return Done, nil
		}
		return Pending, nil
	}

	if w.prepared == nil || w.prepared.Release.Build != candidate.Build || w.preparedInstalledBuild != installed.Build {
		w.logger().Info("application replacement available", "harness", w.id(), "installed_build", installed.Build, "available_build", candidate.Build, "version", candidate.Version, "forced", force)
		prepared, err := w.Installer.Prepare(ctx, candidate)
		if err != nil {
			w.clearPrepared()
			return Done, err
		}
		w.prepared = &prepared
		w.preparedInstalledBuild = installed.Build
	}

	report, err := w.Activity.Detect(ctx)
	if err != nil {
		return Pending, fmt.Errorf("inspect %s activity: %w", w.name(), err)
	}
	w.logWarnings(report)
	idleSince := w.observeActivity(report)
	if report.Active() {
		work := strings.Join(report.ActiveThreads, "\x00")
		if work != w.lastActiveWork {
			w.logger().Info("waiting for active work to finish", "harness", w.id(), "work", report.ActiveThreads)
			w.lastActiveWork = work
		}
		return Pending, nil
	}
	w.lastActiveWork = ""
	if !idleSince.IsZero() {
		remaining := w.IdleWindow - w.now().Sub(idleSince)
		if remaining > 0 {
			w.logger().Info("waiting for uninterrupted idle window", "harness", w.id(), "idle_since", idleSince.Format(time.RFC3339), "remaining", remaining.Round(time.Second))
			return Pending, nil
		}
	}

	installed, err = w.Inspector.Inspect(ctx, w.AppPath, false)
	if err != nil {
		return Pending, fmt.Errorf("reinspect installed %s: %w", w.name(), err)
	}
	comparison, err = release.Compare(installed.Build, candidate.Build)
	if err != nil {
		return Done, err
	}
	if comparison > 0 || comparison == 0 && !force {
		w.logger().Info("application updated independently while watcher waited", "harness", w.id(), "installed_build", installed.Build)
		if err := w.removePrepared(); err != nil {
			return Done, err
		}
		return Done, nil
	}
	if installed.Build != w.preparedInstalledBuild {
		w.logger().Info("installed application changed while replacement was staged; abandoning stale work", "harness", w.id(), "previous_build", w.preparedInstalledBuild, "installed_build", installed.Build)
		if err := w.removePrepared(); err != nil {
			return Done, err
		}
		return Pending, nil
	}

	preflight := func(ctx context.Context) error {
		report, err := w.Activity.Detect(ctx)
		if err != nil {
			return err
		}
		w.logWarnings(report)
		idleSince := w.observeActivity(report)
		if report.Active() {
			return fmt.Errorf("work became active: %v", report.ActiveThreads)
		}
		if !idleSince.IsZero() && w.now().Sub(idleSince) < w.IdleWindow {
			return fmt.Errorf("idle window restarted at %s", idleSince.Format(time.RFC3339))
		}
		current, err := w.Inspector.Inspect(ctx, w.AppPath, false)
		if err != nil {
			return err
		}
		if current.Build != w.preparedInstalledBuild {
			return fmt.Errorf("installed build changed from %s to %s", w.preparedInstalledBuild, current.Build)
		}
		return nil
	}
	if err := w.Installer.Apply(ctx, *w.prepared, preflight); err != nil {
		w.clearPrepared()
		return Done, err
	}
	w.clearPrepared()
	return Updated, nil
}

func (w *Watcher) clearPrepared() {
	w.prepared = nil
	w.preparedInstalledBuild = ""
}

func (w *Watcher) removePrepared() error {
	if w.prepared != nil {
		if err := update.RemovePrepared(*w.prepared); err != nil {
			return fmt.Errorf("remove abandoned %s replacement: %w", w.name(), err)
		}
	}
	w.clearPrepared()
	return nil
}

func (w *Watcher) logWarnings(report activity.Report) {
	for _, warning := range report.Warnings {
		w.logger().Warn("activity record was skipped", "harness", w.id(), "warning", warning)
	}
}

func (w *Watcher) observeActivity(report activity.Report) time.Time {
	if report.Active() {
		w.observedActive = true
		w.idleSince = time.Time{}
		return time.Time{}
	}
	if w.observedActive {
		w.observedActive = false
		w.idleSince = w.now()
	}
	if report.LastLifecycle.After(w.idleSince) {
		return report.LastLifecycle
	}
	return w.idleSince
}

func (w *Watcher) validate() error {
	if w.Feed == nil || w.Activity == nil || w.Inspector == nil || w.Installer == nil {
		return fmt.Errorf("%s watcher dependencies are incomplete", w.name())
	}
	if w.IdleWindow <= 0 || w.PollInterval <= 0 || w.ActivityPollInterval <= 0 {
		return fmt.Errorf("watch intervals must be positive")
	}
	return nil
}

func (w *Watcher) id() string {
	if w.ID != "" {
		return w.ID
	}
	return "chatgpt"
}

func (w *Watcher) name() string {
	if w.Name != "" {
		return w.Name
	}
	return "ChatGPT Desktop"
}

func (w *Watcher) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
}

func (w *Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

type Coordinator struct {
	Watchers             []*Watcher
	PollInterval         time.Duration
	ActivityPollInterval time.Duration
	Logger               *slog.Logger
	Sleep                func(context.Context, time.Duration) error
}

func (c Coordinator) Run(ctx context.Context, once, force bool) error {
	if len(c.Watchers) == 0 {
		return nil
	}
	if c.PollInterval <= 0 || c.ActivityPollInterval <= 0 {
		return fmt.Errorf("coordinator intervals must be positive")
	}
	completed := make([]bool, len(c.Watchers))
	var oneShotErrors []error
	for {
		pending := false
		allDone := true
		for index, watcher := range c.Watchers {
			if once && completed[index] {
				continue
			}
			state, err := watcher.Step(ctx, force)
			if err != nil {
				wrapped := fmt.Errorf("%s: %w", watcher.id(), err)
				if once {
					oneShotErrors = append(oneShotErrors, wrapped)
					completed[index] = true
					continue
				}
				c.logger().Error("watch cycle failed", "harness", watcher.id(), "error", err)
				continue
			}
			switch state {
			case Pending:
				pending = true
				allDone = false
			case Updated:
				c.logger().Info("watch cycle installed an application replacement", "harness", watcher.id(), "forced", force)
				if once {
					completed[index] = true
				}
			case Done:
				if once {
					completed[index] = true
				}
			}
		}
		if once {
			allDone = true
			for _, done := range completed {
				allDone = allDone && done
			}
			if allDone {
				return errors.Join(oneShotErrors...)
			}
		}
		delay := c.PollInterval
		if pending || once && !allDone {
			delay = c.ActivityPollInterval
		}
		if err := c.sleep(ctx, delay); err != nil {
			return err
		}
	}
}

func (c Coordinator) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c Coordinator) sleep(ctx context.Context, duration time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, duration)
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
