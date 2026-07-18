package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

	mu      sync.Mutex
	blocked map[int64]string
}

type failureRecord struct {
	Build    int64     `json:"build"`
	Version  string    `json:"version"`
	FailedAt time.Time `json:"failed_at"`
	Error    string    `json:"error"`
}

func (i *Installer) Prepare(ctx context.Context, release appcast.Release) (Prepared, error) {
	if err := i.validate(); err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(i.CacheDir, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("create cache directory: %w", err)
	}
	stagedPath := i.stagedPath(release.Build)
	if reason, blocked := i.failureReason(release.Build); blocked {
		if err := i.cleanupResidue(""); err != nil {
			return Prepared{}, err
		}
		return Prepared{}, fmt.Errorf("build %d is quarantined after a failed activation (%s); a newer build will be tried automatically, or remove %s to retry", release.Build, reason, i.failurePath(release.Build))
	}
	if err := i.cleanupResidue(stagedPath); err != nil {
		return Prepared{}, err
	}
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

func (i *Installer) Apply(ctx context.Context, prepared Prepared, preflight func(context.Context) error) error {
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
	current, err := i.inspector().Inspect(ctx, i.AppPath, true)
	if err != nil {
		return fmt.Errorf("verify installed app before replacement: %w", err)
	}
	if preflight != nil {
		if err := preflight(ctx); err != nil {
			return fmt.Errorf("final activity check: %w", err)
		}
	}

	application, err := i.processes().DesktopApplication(ctx, i.AppPath)
	if err != nil {
		return err
	}
	if application != nil {
		i.logger().Info("requesting graceful ChatGPT Desktop shutdown", "pid", application.PID)
		output, quitErr := i.runner().CombinedOutput(ctx, "/usr/bin/osascript", "-e", `tell application id "com.openai.codex" to quit`)
		if quitErr != nil {
			return i.relaunchPrevious(ctx, current, commandError("request ChatGPT Desktop quit", output, quitErr))
		}
		if err := i.waitForExit(ctx); err != nil {
			return err
		}
	}

	backupPath := filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-backup-%d-%d", filepath.Base(i.AppPath), current.Build, time.Now().UnixNano()))
	if err := os.Rename(i.AppPath, backupPath); err != nil {
		return i.abortActivation(ctx, prepared, current, fmt.Errorf("move installed app to rollback path: %w", err))
	}
	if err := os.Rename(prepared.StagedPath, i.AppPath); err != nil {
		restoreErr := os.Rename(backupPath, i.AppPath)
		if restoreErr != nil {
			return fmt.Errorf("activate staged app: %w (rollback also failed: %v)", err, restoreErr)
		}
		return i.abortActivation(ctx, prepared, current, fmt.Errorf("activate staged app: %w", err))
	}

	if err := i.launchAndWait(ctx, prepared.Release.Build); err != nil {
		return i.rollback(ctx, backupPath, prepared, current, err)
	}
	if err := removeExact(backupPath, filepath.Dir(i.AppPath)); err != nil {
		i.logger().Warn("updated app is running but rollback bundle could not be removed", "path", backupPath, "error", err)
	}
	i.logger().Info("ChatGPT Desktop update completed", "old_build", current.Build, "new_build", prepared.Release.Build, "version", prepared.Release.Version)
	return nil
}

func (i *Installer) download(ctx context.Context, release appcast.Release, archivePath string) error {
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

func (i *Installer) waitForExit(ctx context.Context) error {
	deadline := time.Now().Add(i.QuitTimeout)
	for {
		application, err := i.processes().DesktopApplication(ctx, i.AppPath)
		if err != nil {
			return err
		}
		if application == nil {
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

func (i *Installer) launchAndWait(ctx context.Context, expectedBuild int64) error {
	output, err := i.runner().CombinedOutput(ctx, "/usr/bin/open", i.AppPath)
	if err != nil {
		return commandError("launch updated ChatGPT Desktop", output, err)
	}
	deadline := time.Now().Add(i.LaunchTimeout)
	for {
		application, err := i.processes().DesktopApplication(ctx, i.AppPath)
		if err != nil {
			return err
		}
		if application != nil {
			bundle, inspectErr := i.inspector().Inspect(ctx, i.AppPath, true)
			if inspectErr == nil && bundle.Build == expectedBuild {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("updated ChatGPT Desktop application did not become ready within %s", i.LaunchTimeout)
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
}

func (i *Installer) rollback(ctx context.Context, backupPath string, prepared Prepared, previous macos.Bundle, cause error) error {
	i.logger().Error("updated app failed readiness check; rolling back", "error", cause)
	if application, err := i.processes().DesktopApplication(ctx, i.AppPath); err == nil && application != nil {
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
	markerErr := i.markFailure(prepared.Release, cause)
	cleanupErr := removeExact(failedPath, filepath.Dir(i.AppPath))
	relaunchErr := i.launchAndWait(ctx, previous.Build)
	result := fmt.Errorf("%w; previous app restored and build %d quarantined", cause, prepared.Release.Build)
	if markerErr != nil {
		result = fmt.Errorf("%w; could not persist quarantine marker: %v", result, markerErr)
	}
	if cleanupErr != nil {
		result = fmt.Errorf("%w; could not remove failed replacement %s: %v", result, failedPath, cleanupErr)
	}
	if relaunchErr != nil {
		result = fmt.Errorf("%w; previous app relaunch did not become ready: %v", result, relaunchErr)
	}
	return result
}

func (i *Installer) relaunchPrevious(ctx context.Context, previous macos.Bundle, cause error) error {
	if err := i.launchAndWait(ctx, previous.Build); err != nil {
		return fmt.Errorf("%w; previous app relaunch did not become ready: %v", cause, err)
	}
	return fmt.Errorf("%w; previous app relaunched", cause)
}

func (i *Installer) abortActivation(ctx context.Context, prepared Prepared, previous macos.Bundle, cause error) error {
	markerErr := i.markFailure(prepared.Release, cause)
	cleanupErr := removeExact(prepared.StagedPath, filepath.Dir(i.AppPath))
	result := i.relaunchPrevious(ctx, previous, cause)
	result = fmt.Errorf("%w; build %d quarantined", result, prepared.Release.Build)
	if markerErr != nil {
		result = fmt.Errorf("%w; could not persist quarantine marker: %v", result, markerErr)
	}
	if cleanupErr != nil {
		result = fmt.Errorf("%w; could not remove staged replacement: %v", result, cleanupErr)
	}
	return result
}

func (i *Installer) markFailure(release appcast.Release, cause error) error {
	record := failureRecord{Build: release.Build, Version: release.Version, FailedAt: time.Now().UTC(), Error: cause.Error()}
	i.rememberFailure(release.Build, record.Error)

	finish := func(err error) error {
		if err != nil {
			return err
		}
		i.mu.Lock()
		delete(i.blocked, release.Build)
		i.mu.Unlock()
		return nil
	}

	if err := os.MkdirAll(i.CacheDir, 0o700); err != nil {
		return finish(fmt.Errorf("create cache directory for failure marker: %w", err))
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return finish(fmt.Errorf("encode failure marker: %w", err))
	}
	temporaryPath := i.failurePath(release.Build) + ".partial-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temporaryPath, append(data, '\n'), 0o600); err != nil {
		return finish(fmt.Errorf("write failure marker: %w", err))
	}
	if err := os.Rename(temporaryPath, i.failurePath(release.Build)); err != nil {
		_ = os.Remove(temporaryPath)
		return finish(fmt.Errorf("finish failure marker: %w", err))
	}
	return finish(nil)
}

func (i *Installer) rememberFailure(build int64, reason string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.blocked == nil {
		i.blocked = make(map[int64]string)
	}
	i.blocked[build] = reason
}

func (i *Installer) failureReason(build int64) (string, bool) {
	data, err := os.ReadFile(i.failurePath(build))
	if err == nil {
		var record failureRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return "unreadable failure marker", true
		}
		return record.Error, true
	}
	if !os.IsNotExist(err) {
		return fmt.Sprintf("failure marker cannot be read: %v", err), true
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if reason, ok := i.blocked[build]; ok {
		return reason, true
	}
	return "", false
}

func (i *Installer) cleanupResidue(keepStagedPath string) error {
	parent := filepath.Dir(i.AppPath)
	patterns := []string{
		filepath.Join(parent, "."+filepath.Base(i.AppPath)+".codex-autoupdate-*.new"),
		filepath.Join(parent, "."+filepath.Base(i.AppPath)+".codex-autoupdate-failed-*"),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("find update residue: %w", err)
		}
		for _, path := range matches {
			if path == keepStagedPath {
				continue
			}
			if err := removeExact(path, parent); err != nil {
				return fmt.Errorf("remove update residue %s: %w", path, err)
			}
		}
	}
	return nil
}

func (i *Installer) failurePath(build int64) string {
	return filepath.Join(i.CacheDir, fmt.Sprintf("failed-build-%d.json", build))
}

func (i *Installer) validate() error {
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

func (i *Installer) stagedPath(build int64) string {
	return filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-%d.new", filepath.Base(i.AppPath), build))
}

func (i *Installer) runner() macos.Runner {
	if i.Runner != nil {
		return i.Runner
	}
	return macos.ExecRunner{}
}

func (i *Installer) inspector() macos.Inspector {
	if i.Inspector.Runner != nil {
		return i.Inspector
	}
	return macos.Inspector{Runner: i.runner()}
}

func (i *Installer) processes() macos.ProcessFinder {
	if i.Processes.Runner != nil {
		return i.Processes
	}
	return macos.ProcessFinder{Runner: i.runner()}
}

func (i *Installer) logger() *slog.Logger {
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
