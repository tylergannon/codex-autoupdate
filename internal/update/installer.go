package update

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/appcast"
	"github.com/tylergannon/codex-autoupdate/internal/macos"
)

type Prepared struct {
	Release    appcast.Release
	StagedPath string
}

type Installer struct {
	AppPath       string
	CacheDir      string
	QuitTimeout   time.Duration
	LaunchTimeout time.Duration
	HTTPClient    *http.Client
	Runner        macos.Runner
	Inspector     macos.Inspector
	Processes     macos.ProcessFinder
	Logger        *slog.Logger
}

func (i Installer) Prepare(ctx context.Context, release appcast.Release) (Prepared, error) {
	if err := i.validate(); err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(i.CacheDir, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("create cache directory: %w", err)
	}
	stagedPath := i.stagedPath(release.Build)
	if bundle, err := i.inspector().Inspect(ctx, stagedPath, true); err == nil {
		if err := matchesRelease(bundle, release); err == nil {
			i.logger().Info("reusing verified staged update", "build", release.Build, "path", stagedPath)
			return Prepared{Release: release, StagedPath: stagedPath}, nil
		}
	}
	if err := removeExact(stagedPath, filepath.Dir(i.AppPath)); err != nil {
		return Prepared{}, fmt.Errorf("remove unusable staged update: %w", err)
	}

	archivePath := filepath.Join(i.CacheDir, fmt.Sprintf("ChatGPT-%d.zip", release.Build))
	if err := i.download(ctx, release, archivePath); err != nil {
		return Prepared{}, err
	}
	extractDir, err := os.MkdirTemp(i.CacheDir, fmt.Sprintf("extract-%d-", release.Build))
	if err != nil {
		return Prepared{}, fmt.Errorf("create extraction directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(extractDir) }()
	output, err := i.runner().CombinedOutput(ctx, "/usr/bin/ditto", "-x", "-k", archivePath, extractDir)
	if err != nil {
		return Prepared{}, commandError("extract update archive", output, err)
	}
	extractedApp, err := findExtractedApp(extractDir)
	if err != nil {
		return Prepared{}, err
	}
	bundle, err := i.inspector().Inspect(ctx, extractedApp, true)
	if err != nil {
		return Prepared{}, fmt.Errorf("verify extracted update: %w", err)
	}
	if err := matchesRelease(bundle, release); err != nil {
		return Prepared{}, err
	}
	if output, err := i.runner().CombinedOutput(ctx, "/usr/bin/ditto", extractedApp, stagedPath); err != nil {
		return Prepared{}, commandError("copy update to application volume", output, err)
	}
	stagedBundle, err := i.inspector().Inspect(ctx, stagedPath, true)
	if err != nil {
		return Prepared{}, fmt.Errorf("verify staged update: %w", err)
	}
	if err := matchesRelease(stagedBundle, release); err != nil {
		return Prepared{}, err
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		i.logger().Warn("could not remove downloaded archive", "path", archivePath, "error", err)
	}
	i.logger().Info("update staged and verified", "build", release.Build, "version", release.Version, "path", stagedPath)
	return Prepared{Release: release, StagedPath: stagedPath}, nil
}

func (i Installer) Apply(ctx context.Context, prepared Prepared, preflight func(context.Context) error) error {
	if err := i.validate(); err != nil {
		return err
	}
	bundle, err := i.inspector().Inspect(ctx, prepared.StagedPath, true)
	if err != nil {
		return fmt.Errorf("reverify staged update: %w", err)
	}
	if err := matchesRelease(bundle, prepared.Release); err != nil {
		return err
	}
	if preflight != nil {
		if err := preflight(ctx); err != nil {
			return fmt.Errorf("final activity check: %w", err)
		}
	}

	processes, err := i.processes().BundleProcesses(ctx, i.AppPath)
	if err != nil {
		return err
	}
	if len(processes) > 0 {
		i.logger().Info("requesting graceful ChatGPT Desktop shutdown", "processes", len(processes))
		output, quitErr := i.runner().CombinedOutput(ctx, "/usr/bin/osascript", "-e", `tell application id "com.openai.codex" to quit`)
		if quitErr != nil {
			return commandError("request ChatGPT Desktop quit", output, quitErr)
		}
		if err := i.waitForExit(ctx); err != nil {
			return err
		}
	}

	current, err := i.inspector().Inspect(ctx, i.AppPath, true)
	if err != nil {
		return fmt.Errorf("verify installed app before replacement: %w", err)
	}
	backupPath := filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-backup-%d-%d", filepath.Base(i.AppPath), current.Build, time.Now().UnixNano()))
	if err := os.Rename(i.AppPath, backupPath); err != nil {
		return fmt.Errorf("move installed app to rollback path: %w", err)
	}
	if err := os.Rename(prepared.StagedPath, i.AppPath); err != nil {
		restoreErr := os.Rename(backupPath, i.AppPath)
		if restoreErr != nil {
			return fmt.Errorf("activate staged app: %w (rollback also failed: %v)", err, restoreErr)
		}
		return fmt.Errorf("activate staged app: %w", err)
	}

	if err := i.launchAndWait(ctx, prepared.Release.Build); err != nil {
		return i.rollback(ctx, backupPath, prepared, err)
	}
	if err := removeExact(backupPath, filepath.Dir(i.AppPath)); err != nil {
		i.logger().Warn("updated app is running but rollback bundle could not be removed", "path", backupPath, "error", err)
	}
	i.logger().Info("ChatGPT Desktop update completed", "old_build", current.Build, "new_build", prepared.Release.Build, "version", prepared.Release.Version)
	return nil
}

func (i Installer) download(ctx context.Context, release appcast.Release, archivePath string) error {
	if info, err := os.Stat(archivePath); err == nil && info.Size() == release.Length {
		return nil
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove incomplete update archive: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return fmt.Errorf("create update download request: %w", err)
	}
	client := i.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: unexpected HTTP status %s", resp.Status)
	}
	temporaryPath := archivePath + ".partial-" + strconv.Itoa(os.Getpid())
	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create update archive: %w", err)
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, release.Length+1))
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("write update archive: %w", copyErr)
	}
	if written != release.Length {
		return fmt.Errorf("downloaded update length %d does not match appcast length %d", written, release.Length)
	}
	if err := os.Rename(temporaryPath, archivePath); err != nil {
		return fmt.Errorf("finish update download: %w", err)
	}
	removeTemporary = false
	return nil
}

func (i Installer) waitForExit(ctx context.Context) error {
	deadline := time.Now().Add(i.QuitTimeout)
	for {
		processes, err := i.processes().BundleProcesses(ctx, i.AppPath)
		if err != nil {
			return err
		}
		if len(processes) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ChatGPT Desktop did not exit gracefully within %s; update aborted", i.QuitTimeout)
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

func (i Installer) launchAndWait(ctx context.Context, expectedBuild int64) error {
	output, err := i.runner().CombinedOutput(ctx, "/usr/bin/open", i.AppPath)
	if err != nil {
		return commandError("launch updated ChatGPT Desktop", output, err)
	}
	deadline := time.Now().Add(i.LaunchTimeout)
	for {
		server, err := i.processes().DesktopAppServer(ctx, i.AppPath)
		if err != nil {
			return err
		}
		if server != nil {
			bundle, inspectErr := i.inspector().Inspect(ctx, i.AppPath, true)
			if inspectErr == nil && bundle.Build == expectedBuild {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("updated ChatGPT Desktop app-server did not become ready within %s", i.LaunchTimeout)
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
}

func (i Installer) rollback(ctx context.Context, backupPath string, prepared Prepared, cause error) error {
	i.logger().Error("updated app failed readiness check; rolling back", "error", cause)
	if processes, err := i.processes().BundleProcesses(ctx, i.AppPath); err == nil && len(processes) > 0 {
		_, _ = i.runner().CombinedOutput(ctx, "/usr/bin/osascript", "-e", `tell application id "com.openai.codex" to quit`)
		_ = i.waitForExit(ctx)
	}
	failedPath := filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-failed-%d-%d", filepath.Base(i.AppPath), prepared.Release.Build, time.Now().UnixNano()))
	if err := os.Rename(i.AppPath, failedPath); err != nil {
		return fmt.Errorf("%w; rollback could not move failed app: %v", cause, err)
	}
	if err := os.Rename(backupPath, i.AppPath); err != nil {
		return fmt.Errorf("%w; rollback could not restore previous app: %v", cause, err)
	}
	if output, err := i.runner().CombinedOutput(ctx, "/usr/bin/open", i.AppPath); err != nil {
		return fmt.Errorf("%w; previous app was restored but relaunch failed: %s", cause, strings.TrimSpace(string(output)))
	}
	return fmt.Errorf("%w; previous app restored, failed replacement retained at %s", cause, failedPath)
}

func (i Installer) validate() error {
	if !filepath.IsAbs(i.AppPath) || filepath.Base(i.AppPath) != "ChatGPT.app" {
		return fmt.Errorf("app path must be an absolute path ending in ChatGPT.app")
	}
	if !filepath.IsAbs(i.CacheDir) {
		return fmt.Errorf("cache directory must be absolute")
	}
	if i.QuitTimeout <= 0 || i.LaunchTimeout <= 0 {
		return fmt.Errorf("quit and launch timeouts must be positive")
	}
	info, err := os.Lstat(i.AppPath)
	if err != nil {
		return fmt.Errorf("inspect installed app path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("app path must not be a symbolic link")
	}
	return nil
}

func (i Installer) stagedPath(build int64) string {
	return filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-%d.new", filepath.Base(i.AppPath), build))
}

func (i Installer) runner() macos.Runner {
	if i.Runner != nil {
		return i.Runner
	}
	return macos.ExecRunner{}
}

func (i Installer) inspector() macos.Inspector {
	if i.Inspector.Runner != nil {
		return i.Inspector
	}
	return macos.Inspector{Runner: i.runner()}
}

func (i Installer) processes() macos.ProcessFinder {
	if i.Processes.Runner != nil {
		return i.Processes
	}
	return macos.ProcessFinder{Runner: i.runner()}
}

func (i Installer) logger() *slog.Logger {
	if i.Logger != nil {
		return i.Logger
	}
	return slog.Default()
}

func matchesRelease(bundle macos.Bundle, release appcast.Release) error {
	if bundle.Identifier != macos.BundleIdentifier {
		return fmt.Errorf("staged bundle identifier %q does not match %q", bundle.Identifier, macos.BundleIdentifier)
	}
	if bundle.TeamID != macos.OpenAITeamID {
		return fmt.Errorf("staged signing team %q does not match OpenAI team %q", bundle.TeamID, macos.OpenAITeamID)
	}
	if bundle.Build != release.Build {
		return fmt.Errorf("staged build %d does not match advertised build %d", bundle.Build, release.Build)
	}
	if release.Version != "" && bundle.Version != release.Version {
		return fmt.Errorf("staged version %q does not match advertised version %q", bundle.Version, release.Version)
	}
	return nil
}

func findExtractedApp(root string) (string, error) {
	direct := filepath.Join(root, "ChatGPT.app")
	if info, err := os.Stat(direct); err == nil && info.IsDir() {
		return direct, nil
	}
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "ChatGPT.app" {
			if found != "" {
				return fmt.Errorf("update archive contains multiple ChatGPT.app bundles")
			}
			found = path
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("search extracted update: %w", err)
	}
	if found == "" {
		return "", fmt.Errorf("update archive does not contain ChatGPT.app")
	}
	return found, nil
}

func removeExact(path, allowedParent string) error {
	if filepath.Dir(filepath.Clean(path)) != filepath.Clean(allowedParent) || filepath.Base(path) == "." {
		return fmt.Errorf("refusing to remove path outside %s: %s", allowedParent, path)
	}
	return os.RemoveAll(path)
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func commandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}
