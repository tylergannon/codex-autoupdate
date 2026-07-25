package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tylergannon/codex-autoupdate/internal/activity"
	"github.com/tylergannon/codex-autoupdate/internal/appcast"
	"github.com/tylergannon/codex-autoupdate/internal/claudefeed"
	"github.com/tylergannon/codex-autoupdate/internal/launchagent"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
	"github.com/tylergannon/codex-autoupdate/internal/release"
	"github.com/tylergannon/codex-autoupdate/internal/runlock"
	"github.com/tylergannon/codex-autoupdate/internal/update"
	"github.com/tylergannon/codex-autoupdate/internal/watch"
)

const (
	chatGPT = "chatgpt"
	claude  = "claude"
)

var defaultHarnesses = []string{chatGPT, claude}

type settings struct {
	chatGPTAppPath       string
	claudeAppPath        string
	codexHome            string
	claudeData           string
	cacheDir             string
	feedURL              string
	claudeFeedURL        string
	harnesses            []string
	idleWindow           time.Duration
	pollInterval         time.Duration
	activityPollInterval time.Duration
	quitTimeout          time.Duration
	launchTimeout        time.Duration
}

func NewRoot(version string, stdout, stderr io.Writer) (*cobra.Command, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("codex-autoupdate supports macOS only")
	}
	current, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("resolve current user: %w", err)
	}
	config := settings{
		chatGPTAppPath:       "/Applications/ChatGPT.app",
		claudeAppPath:        "/Applications/Claude.app",
		codexHome:            filepath.Join(current.HomeDir, ".codex"),
		claudeData:           filepath.Join(current.HomeDir, "Library", "Application Support", "Claude"),
		cacheDir:             filepath.Join(current.HomeDir, "Library", "Caches", "codex-autoupdate"),
		feedURL:              appcast.DefaultURL,
		claudeFeedURL:        claudefeed.DefaultURL,
		idleWindow:           5 * time.Minute,
		pollInterval:         15 * time.Minute,
		activityPollInterval: 5 * time.Second,
		quitTimeout:          30 * time.Second,
		launchTimeout:        90 * time.Second,
	}
	root := &cobra.Command{
		Use:           "codex-autoupdate",
		Short:         "Update desktop coding harnesses after their work becomes idle",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	flags := root.PersistentFlags()
	flags.StringVar(&config.chatGPTAppPath, "app-path", config.chatGPTAppPath, "path to ChatGPT.app (compatibility alias)")
	flags.StringVar(&config.chatGPTAppPath, "chatgpt-app-path", config.chatGPTAppPath, "path to ChatGPT.app")
	flags.StringVar(&config.claudeAppPath, "claude-app-path", config.claudeAppPath, "path to Claude.app")
	flags.StringVar(&config.codexHome, "codex-home", config.codexHome, "Codex state directory")
	flags.StringVar(&config.claudeData, "claude-data", config.claudeData, "Claude Desktop state directory")
	flags.StringVar(&config.cacheDir, "cache-dir", config.cacheDir, "download and lock directory")
	flags.StringVar(&config.feedURL, "feed-url", config.feedURL, "ChatGPT Sparkle appcast URL")
	flags.StringVar(&config.claudeFeedURL, "claude-feed-url", config.claudeFeedURL, "Claude Desktop update feed URL")
	flags.StringSliceVar(&config.harnesses, "harness", nil, "enabled harness (chatgpt or claude; repeat or comma-separate)")
	flags.DurationVar(&config.idleWindow, "idle-window", config.idleWindow, "required uninterrupted period with no running harness work")
	flags.DurationVar(&config.pollInterval, "poll-interval", config.pollInterval, "interval between update checks")
	flags.DurationVar(&config.activityPollInterval, "activity-poll-interval", config.activityPollInterval, "interval between activity checks while an update is pending")
	flags.DurationVar(&config.quitTimeout, "quit-timeout", config.quitTimeout, "maximum wait for graceful application shutdown")
	flags.DurationVar(&config.launchTimeout, "launch-timeout", config.launchTimeout, "maximum wait for an updated application")

	root.AddCommand(
		newRunCommand(&config),
		newUpdateCommand(&config),
		newCheckCommand(&config),
		newInstallCommand(&config),
		newUninstallCommand(),
		newStatusCommand(),
	)
	return root, nil
}

func newRunCommand(config *settings) *cobra.Command {
	var once bool
	command := &cobra.Command{
		Use:   "run",
		Short: "Run the multi-harness update watcher in the foreground",
		RunE: func(command *cobra.Command, _ []string) error {
			return config.run(command, once, false)
		},
	}
	command.Flags().BoolVar(&once, "once", false, "finish all eligible harness checks and exit")
	return command
}

func newUpdateCommand(config *settings) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "update",
		Short: "Run one update or safe forced-reinstallation pass",
		RunE: func(command *cobra.Command, _ []string) error {
			return config.run(command, true, force)
		},
	}
	command.Flags().BoolVar(&force, "force", false, "reinstall the latest equal version without permitting downgrade or bypassing safety checks")
	return command
}

func (s settings) run(command *cobra.Command, once, force bool) (resultErr error) {
	if err := s.validate(); err != nil {
		return err
	}
	var wakeSignals chan os.Signal
	if !once {
		wakeSignals = make(chan os.Signal, 1)
		signal.Notify(wakeSignals, syscall.SIGUSR1)
		defer signal.Stop(wakeSignals)
	}
	lock, takeover, err := acquireRunLock(command.Context(), s.cacheDir, once, nil)
	if err != nil {
		if errors.Is(err, runlock.ErrYieldRequested) {
			return nil
		}
		return err
	}
	logger := slog.New(slog.NewTextHandler(command.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
	defer func() {
		closeErr := lock.Close()
		takeoverErr := takeover.Close()
		resultErr = errors.Join(resultErr, closeErr, takeoverErr)
	}()
	watchers, err := s.watchers(command.Context(), logger)
	if err != nil {
		return err
	}
	coordinator := watch.Coordinator{
		Watchers:             watchers,
		PollInterval:         s.pollInterval,
		ActivityPollInterval: s.activityPollInterval,
		Logger:               logger,
	}
	if wakeSignals != nil {
		coordinator.Sleep = func(ctx context.Context, duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			case <-wakeSignals:
				requested, err := runlock.TakeoverRequested(s.cacheDir)
				if err != nil {
					return err
				}
				if requested {
					return runlock.ErrYieldRequested
				}
				return nil
			}
		}
	}
	err = coordinator.Run(command.Context(), once, force)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if errors.Is(err, runlock.ErrYieldRequested) {
		return nil
	}
	return err
}

func acquireRunLock(ctx context.Context, cacheDir string, oneShot bool, wake func(int) error) (*runlock.Lock, *runlock.Takeover, error) {
	if !oneShot {
		requested, err := runlock.TakeoverRequested(cacheDir)
		if err != nil {
			return nil, nil, err
		}
		if requested {
			return nil, nil, runlock.ErrYieldRequested
		}
	}
	acquire := runlock.Acquire
	if !oneShot {
		acquire = runlock.AcquireDaemon
	}
	lock, err := acquire(cacheDir)
	if err == nil || !oneShot || !errors.Is(err, runlock.ErrAlreadyRunning) {
		return lock, nil, err
	}
	takeover, err := runlock.RequestTakeover(cacheDir, wake)
	if err != nil {
		return nil, nil, errors.Join(runlock.ErrAlreadyRunning, err)
	}
	const (
		retryInterval = 100 * time.Millisecond
		retryTimeout  = 10 * time.Second
	)
	deadline := time.Now().Add(retryTimeout)
	for {
		lock, err = runlock.Acquire(cacheDir)
		if err == nil {
			return lock, takeover, nil
		}
		if !errors.Is(err, runlock.ErrAlreadyRunning) || time.Now().After(deadline) {
			return nil, nil, errors.Join(err, takeover.Close())
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, errors.Join(ctx.Err(), takeover.Close())
		case <-timer.C:
		}
	}
}

func newCheckCommand(config *settings) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "check",
		Short: "Report installed, available, and activity state without changing anything",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := config.validate(); err != nil {
				return err
			}
			results, resultErr := config.check(command.Context())
			if asJSON {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(struct {
					Harnesses []checkResult `json:"harnesses"`
				}{results}); err != nil {
					return err
				}
			} else {
				for _, result := range results {
					if !result.Installed {
						_, _ = fmt.Fprintf(command.OutOrStdout(), "%s: not installed (skipped)\n", result.ID)
						continue
					}
					if result.Error != "" {
						_, _ = fmt.Fprintf(command.OutOrStdout(), "%s: error: %s\n", result.ID, result.Error)
						continue
					}
					_, _ = fmt.Fprintf(command.OutOrStdout(), "%s: installed %s, available %s, update available: %t, active work: %d\n",
						result.ID, result.InstalledBundle.Build, result.Available.Build, result.UpdateAvailable, len(result.Activity.ActiveThreads))
				}
			}
			return resultErr
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return command
}

type checkResult struct {
	ID              string           `json:"id"`
	Installed       bool             `json:"installed"`
	Path            string           `json:"path"`
	InstalledBundle *macos.Bundle    `json:"installed_bundle,omitempty"`
	Available       *release.Release `json:"available,omitempty"`
	UpdateAvailable bool             `json:"update_available"`
	Activity        *activity.Report `json:"activity,omitempty"`
	Error           string           `json:"error,omitempty"`
}

func (s settings) check(ctx context.Context) ([]checkResult, error) {
	targets, err := s.targets()
	if err != nil {
		return nil, err
	}
	var results []checkResult
	var failures []error
	for _, target := range targets {
		result := checkResult{ID: target.id, Path: target.appPath}
		info, err := os.Stat(target.appPath)
		if os.IsNotExist(err) {
			results = append(results, result)
			continue
		}
		if err != nil || !info.IsDir() {
			if err == nil {
				err = fmt.Errorf("path is not an application bundle directory")
			}
			result.Error = err.Error()
			failures = append(failures, fmt.Errorf("%s: %w", target.id, err))
			results = append(results, result)
			continue
		}
		result.Installed = true
		bundle, inspectErr := target.inspector.Inspect(ctx, target.appPath, false)
		available, feedErr := target.feed.Latest(ctx)
		report, activityErr := target.activity.Detect(ctx)
		if inspectErr == nil {
			result.InstalledBundle = &bundle
		}
		if feedErr == nil {
			result.Available = &available
		}
		if activityErr == nil {
			result.Activity = &report
		}
		err = errors.Join(inspectErr, feedErr, activityErr)
		if err != nil {
			result.Error = err.Error()
			failures = append(failures, fmt.Errorf("%s: %w", target.id, err))
		} else {
			comparison, compareErr := release.Compare(available.Build, bundle.Build)
			if compareErr != nil {
				result.Error = compareErr.Error()
				failures = append(failures, fmt.Errorf("%s: %w", target.id, compareErr))
			} else {
				result.UpdateAvailable = comparison > 0
			}
		}
		results = append(results, result)
	}
	return results, errors.Join(failures...)
}

func newInstallCommand(config *settings) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register and start the installed per-user LaunchAgent",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := config.validate(); err != nil {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve current executable: %w", err)
			}
			executable, err = filepath.EvalSymlinks(executable)
			if err != nil {
				return fmt.Errorf("resolve executable symlinks: %w", err)
			}
			err = (launchagent.Manager{}).Install(command.Context(), launchagent.Config{
				Executable:           executable,
				Harnesses:            config.selectedHarnesses(),
				ChatGPTAppPath:       config.chatGPTAppPath,
				ClaudeAppPath:        config.claudeAppPath,
				CodexHome:            config.codexHome,
				ClaudeData:           config.claudeData,
				CacheDir:             config.cacheDir,
				FeedURL:              config.feedURL,
				ClaudeFeedURL:        config.claudeFeedURL,
				IdleWindow:           config.idleWindow.String(),
				PollInterval:         config.pollInterval.String(),
				ActivityPollInterval: config.activityPollInterval.String(),
				QuitTimeout:          config.quitTimeout.String(),
				LaunchTimeout:        config.launchTimeout.String(),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "registered and started "+launchagent.Label)
			return err
		},
	}
}

func newUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the per-user LaunchAgent",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := (launchagent.Manager{}).Uninstall(command.Context()); err != nil {
				return err
			}
			_, err := fmt.Fprintln(command.OutOrStdout(), "uninstalled "+launchagent.Label+"; logs and cache retained")
			return err
		},
	}
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print launchd's status for the watcher",
		RunE: func(command *cobra.Command, _ []string) error {
			status, err := (launchagent.Manager{}).Status(command.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(command.OutOrStdout(), status)
			return err
		},
	}
}

type target struct {
	id        string
	name      string
	appPath   string
	identity  macos.Identity
	feed      watch.Feed
	activity  watch.Activity
	inspector macos.Inspector
	processes macos.ProcessFinder
}

func (s settings) targets() ([]target, error) {
	runner := macos.ExecRunner{}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	var targets []target
	for _, id := range s.selectedHarnesses() {
		switch id {
		case chatGPT:
			processes := macos.ProcessFinder{Runner: runner, CodexHome: s.codexHome}
			targets = append(targets, target{
				id:        chatGPT,
				name:      macos.ChatGPTIdentity.Name,
				appPath:   s.chatGPTAppPath,
				identity:  macos.ChatGPTIdentity,
				feed:      appcast.Client{HTTPClient: httpClient, FeedURL: s.feedURL},
				activity:  &activity.Detector{AppPath: s.chatGPTAppPath, CodexHome: s.codexHome, Processes: processes},
				inspector: macos.Inspector{Runner: runner, Identity: macos.ChatGPTIdentity},
				processes: processes,
			})
		case claude:
			processes := macos.ProcessFinder{Runner: runner}
			targets = append(targets, target{
				id:        claude,
				name:      macos.ClaudeIdentity.Name,
				appPath:   s.claudeAppPath,
				identity:  macos.ClaudeIdentity,
				feed:      claudefeed.Client{HTTPClient: httpClient, FeedURL: s.claudeFeedURL},
				activity:  activity.ClaudeDetector{AppPath: s.claudeAppPath, ClaudeData: s.claudeData, Processes: processes},
				inspector: macos.Inspector{Runner: runner, Identity: macos.ClaudeIdentity},
				processes: processes,
			})
		default:
			return nil, fmt.Errorf("unknown harness %q", id)
		}
	}
	return targets, nil
}

func (s settings) watchers(ctx context.Context, logger *slog.Logger) ([]*watch.Watcher, error) {
	targets, err := s.targets()
	if err != nil {
		return nil, err
	}
	var result []*watch.Watcher
	for _, target := range targets {
		installer := &update.Installer{
			AppPath:       target.appPath,
			CacheDir:      filepath.Join(s.cacheDir, target.id),
			QuitTimeout:   s.quitTimeout,
			LaunchTimeout: s.launchTimeout,
			HTTPClient:    &http.Client{Timeout: 30 * time.Minute},
			Runner:        macos.ExecRunner{},
			Inspector:     target.inspector,
			Processes:     target.processes,
			Logger:        logger,
			Identity:      target.identity,
		}
		info, err := os.Lstat(target.appPath)
		if os.IsNotExist(err) {
			recovered, recoveryErr := installer.RecoverInterruptedActivation(ctx)
			if recoveryErr != nil {
				return nil, recoveryErr
			}
			if !recovered {
				continue
			}
			info, err = os.Lstat(target.appPath)
		}
		if err != nil {
			return nil, fmt.Errorf("inspect %s path: %w", target.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("%s path must be a non-symbolic-link application bundle directory: %s", target.name, target.appPath)
		}
		result = append(result, &watch.Watcher{
			ID:                   target.id,
			Name:                 target.name,
			AppPath:              target.appPath,
			IdleWindow:           s.idleWindow,
			PollInterval:         s.pollInterval,
			ActivityPollInterval: s.activityPollInterval,
			Feed:                 target.feed,
			Activity:             target.activity,
			Inspector:            target.inspector,
			Installer:            installer,
			Logger:               logger,
		})
	}
	return result, nil
}

func (s settings) selectedHarnesses() []string {
	if len(s.harnesses) == 0 {
		return slices.Clone(defaultHarnesses)
	}
	var result []string
	for _, id := range s.harnesses {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" && !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	return result
}

func (s settings) validate() error {
	if !filepath.IsAbs(s.chatGPTAppPath) || filepath.Base(s.chatGPTAppPath) != "ChatGPT.app" {
		return fmt.Errorf("--chatgpt-app-path/--app-path must be an absolute path ending in ChatGPT.app")
	}
	if !filepath.IsAbs(s.claudeAppPath) || filepath.Base(s.claudeAppPath) != "Claude.app" {
		return fmt.Errorf("--claude-app-path must be an absolute path ending in Claude.app")
	}
	if !filepath.IsAbs(s.codexHome) || !filepath.IsAbs(s.claudeData) || !filepath.IsAbs(s.cacheDir) {
		return fmt.Errorf("--codex-home, --claude-data, and --cache-dir must be absolute paths")
	}
	for _, id := range s.selectedHarnesses() {
		if id != chatGPT && id != claude {
			return fmt.Errorf("--harness must be chatgpt or claude, got %q", id)
		}
	}
	if len(s.selectedHarnesses()) == 0 {
		return fmt.Errorf("at least one --harness must be enabled")
	}
	if s.idleWindow <= 0 || s.pollInterval <= 0 || s.activityPollInterval <= 0 || s.quitTimeout <= 0 || s.launchTimeout <= 0 {
		return fmt.Errorf("all duration flags must be positive")
	}
	return nil
}
