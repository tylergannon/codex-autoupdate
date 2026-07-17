package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"github.com/tylergannon/codex-autoupdate/internal/activity"
	"github.com/tylergannon/codex-autoupdate/internal/appcast"
	"github.com/tylergannon/codex-autoupdate/internal/launchagent"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
	"github.com/tylergannon/codex-autoupdate/internal/runlock"
	"github.com/tylergannon/codex-autoupdate/internal/update"
	"github.com/tylergannon/codex-autoupdate/internal/watch"
)

type settings struct {
	appPath              string
	codexHome            string
	cacheDir             string
	feedURL              string
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
		appPath:              "/Applications/ChatGPT.app",
		codexHome:            filepath.Join(current.HomeDir, ".codex"),
		cacheDir:             filepath.Join(current.HomeDir, "Library", "Caches", "codex-autoupdate"),
		feedURL:              appcast.DefaultURL,
		idleWindow:           5 * time.Minute,
		pollInterval:         15 * time.Minute,
		activityPollInterval: 5 * time.Second,
		quitTimeout:          30 * time.Second,
		launchTimeout:        90 * time.Second,
	}
	root := &cobra.Command{
		Use:           "codex-autoupdate",
		Short:         "Update ChatGPT Desktop after local Codex tasks become idle",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	flags := root.PersistentFlags()
	flags.StringVar(&config.appPath, "app-path", config.appPath, "path to ChatGPT.app")
	flags.StringVar(&config.codexHome, "codex-home", config.codexHome, "Codex state directory")
	flags.StringVar(&config.cacheDir, "cache-dir", config.cacheDir, "download and lock directory")
	flags.StringVar(&config.feedURL, "feed-url", config.feedURL, "Sparkle appcast URL")
	flags.DurationVar(&config.idleWindow, "idle-window", config.idleWindow, "required uninterrupted period with no running Desktop tasks")
	flags.DurationVar(&config.pollInterval, "poll-interval", config.pollInterval, "interval between update checks")
	flags.DurationVar(&config.activityPollInterval, "activity-poll-interval", config.activityPollInterval, "interval between Desktop task activity checks")
	flags.DurationVar(&config.quitTimeout, "quit-timeout", config.quitTimeout, "maximum wait for a graceful Desktop shutdown")
	flags.DurationVar(&config.launchTimeout, "launch-timeout", config.launchTimeout, "maximum wait for the updated Desktop app-server")

	root.AddCommand(newRunCommand(&config), newCheckCommand(&config), newInstallCommand(&config), newUninstallCommand(), newStatusCommand())
	return root, nil
}

func newRunCommand(config *settings) *cobra.Command {
	var once bool
	command := &cobra.Command{
		Use:   "run",
		Short: "Run the update watcher in the foreground",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := config.validate(); err != nil {
				return err
			}
			lock, err := runlock.Acquire(config.cacheDir)
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewTextHandler(command.ErrOrStderr(), &slog.HandlerOptions{Level: slog.LevelInfo}))
			defer func() {
				if err := lock.Close(); err != nil {
					logger.Error("release watcher lock", "error", err)
				}
			}()
			watcher := config.watcher(logger)
			err = watcher.Run(command.Context(), once)
			if err == context.Canceled {
				return nil
			}
			return err
		},
	}
	command.Flags().BoolVar(&once, "once", false, "perform one update check and exit")
	return command
}

func newCheckCommand(config *settings) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "check",
		Short: "Report installed, available, and Desktop task state without changing anything",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := config.validate(); err != nil {
				return err
			}
			ctx := command.Context()
			inspector := macos.Inspector{}
			installed, err := inspector.Inspect(ctx, config.appPath, false)
			if err != nil {
				return err
			}
			feed := appcast.Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}, FeedURL: config.feedURL}
			available, err := feed.Latest(ctx)
			if err != nil {
				return err
			}
			detector := activity.Detector{AppPath: config.appPath, CodexHome: config.codexHome}
			report, err := detector.Detect(ctx)
			if err != nil {
				return err
			}
			result := struct {
				Installed       macos.Bundle    `json:"installed"`
				Available       appcast.Release `json:"available"`
				UpdateAvailable bool            `json:"update_available"`
				Activity        activity.Report `json:"activity"`
			}{installed, available, available.Build > installed.Build, report}
			if asJSON {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(result)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "installed: %s (%d)\navailable: %s (%d)\nupdate available: %t\nactive Desktop tasks: %d\n", installed.Version, installed.Build, available.Version, available.Build, result.UpdateAvailable, len(report.ActiveThreads))
			return err
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return command
}

func newInstallCommand(config *settings) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the per-user LaunchAgent",
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
			manager := launchagent.Manager{}
			err = manager.Install(command.Context(), launchagent.Config{
				Executable:           executable,
				AppPath:              config.appPath,
				CodexHome:            config.codexHome,
				CacheDir:             config.cacheDir,
				FeedURL:              config.feedURL,
				IdleWindow:           config.idleWindow.String(),
				PollInterval:         config.pollInterval.String(),
				ActivityPollInterval: config.activityPollInterval.String(),
				QuitTimeout:          config.quitTimeout.String(),
				LaunchTimeout:        config.launchTimeout.String(),
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "installed and started "+launchagent.Label)
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
			_, err := fmt.Fprintln(command.OutOrStdout(), "uninstalled "+launchagent.Label)
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

func (s settings) watcher(logger *slog.Logger) watch.Watcher {
	runner := macos.ExecRunner{}
	inspector := macos.Inspector{Runner: runner}
	processes := macos.ProcessFinder{Runner: runner}
	detector := activity.Detector{AppPath: s.appPath, CodexHome: s.codexHome, Processes: processes}
	feed := appcast.Client{HTTPClient: &http.Client{Timeout: 30 * time.Second}, FeedURL: s.feedURL}
	installer := update.Installer{
		AppPath:       s.appPath,
		CacheDir:      s.cacheDir,
		QuitTimeout:   s.quitTimeout,
		LaunchTimeout: s.launchTimeout,
		HTTPClient:    &http.Client{Timeout: 30 * time.Minute},
		Runner:        runner,
		Inspector:     inspector,
		Processes:     processes,
		Logger:        logger,
	}
	return watch.Watcher{
		AppPath:              s.appPath,
		IdleWindow:           s.idleWindow,
		PollInterval:         s.pollInterval,
		ActivityPollInterval: s.activityPollInterval,
		Feed:                 feed,
		Activity:             &detector,
		Inspector:            inspector,
		Installer:            installer,
		Logger:               logger,
	}
}

func (s settings) validate() error {
	if !filepath.IsAbs(s.appPath) || filepath.Base(s.appPath) != "ChatGPT.app" {
		return fmt.Errorf("--app-path must be an absolute path ending in ChatGPT.app")
	}
	if !filepath.IsAbs(s.codexHome) || !filepath.IsAbs(s.cacheDir) {
		return fmt.Errorf("--codex-home and --cache-dir must be absolute paths")
	}
	if s.idleWindow <= 0 || s.pollInterval <= 0 || s.activityPollInterval <= 0 || s.quitTimeout <= 0 || s.launchTimeout <= 0 {
		return fmt.Errorf("all duration flags must be positive")
	}
	return nil
}
