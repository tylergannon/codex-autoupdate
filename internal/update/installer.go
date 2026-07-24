package update

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/tylergannon/codex-autoupdate/internal/macos"
	"github.com/tylergannon/codex-autoupdate/internal/release"
)

type Prepared struct {
	Release    release.Release
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
	Identity      macos.Identity

	mu      sync.Mutex
	blocked map[string]string
}

type failureRecord struct {
	Build    string    `json:"build"`
	Version  string    `json:"version"`
	FailedAt time.Time `json:"failed_at"`
	Error    string    `json:"error"`
}

func (i *Installer) Prepare(ctx context.Context, candidate release.Release) (Prepared, error) {
	if err := i.validate(); err != nil {
		return Prepared{}, err
	}
	if !release.IsNumericVersion(candidate.Build) {
		return Prepared{}, fmt.Errorf("invalid release build %q", candidate.Build)
	}
	if err := os.MkdirAll(i.CacheDir, 0o700); err != nil {
		return Prepared{}, fmt.Errorf("create cache directory: %w", err)
	}
	stagedPath := i.stagedPath(candidate.Build)
	if reason, markerPath, blocked := i.failureReason(candidate.Build); blocked {
		if err := i.cleanupResidue(""); err != nil {
			return Prepared{}, err
		}
		return Prepared{}, fmt.Errorf("%s version %s is quarantined after a failed activation (%s); a newer version will be tried automatically, or remove %s to retry", i.identity().Name, candidate.Build, reason, markerPath)
	}
	if err := candidate.Validate(); err != nil {
		return Prepared{}, err
	}
	if err := i.cleanupResidue(stagedPath); err != nil {
		return Prepared{}, err
	}
	if bundle, err := i.inspector().Inspect(ctx, stagedPath, true); err == nil {
		if err := i.matchesRelease(bundle, candidate); err == nil {
			i.logger().Info("reusing verified staged update", "harness", i.identity().Name, "build", candidate.Build, "path", stagedPath)
			return Prepared{Release: candidate, StagedPath: stagedPath}, nil
		}
	}
	if err := removeExact(stagedPath, filepath.Dir(i.AppPath)); err != nil {
		return Prepared{}, fmt.Errorf("remove unusable staged update: %w", err)
	}

	archivePath := filepath.Join(i.CacheDir, fmt.Sprintf("%s-%s.zip", i.identity().Executable, release.Key(candidate.Build)))
	if err := i.download(ctx, candidate, archivePath); err != nil {
		return Prepared{}, err
	}
	extractDir, err := os.MkdirTemp(i.CacheDir, fmt.Sprintf("extract-%s-", release.Key(candidate.Build)))
	if err != nil {
		return Prepared{}, fmt.Errorf("create extraction directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(extractDir) }()
	output, err := i.runner().CombinedOutput(ctx, "/usr/bin/ditto", "-x", "-k", archivePath, extractDir)
	if err != nil {
		return Prepared{}, commandError("extract update archive", output, err)
	}
	extractedApp, err := findExtractedApp(extractDir, filepath.Base(i.AppPath))
	if err != nil {
		return Prepared{}, err
	}
	bundle, err := i.inspector().Inspect(ctx, extractedApp, true)
	if err != nil {
		return Prepared{}, fmt.Errorf("verify extracted update: %w", err)
	}
	if err := i.matchesRelease(bundle, candidate); err != nil {
		return Prepared{}, err
	}
	if output, err := i.runner().CombinedOutput(ctx, "/usr/bin/ditto", extractedApp, stagedPath); err != nil {
		return Prepared{}, commandError("copy update to application volume", output, err)
	}
	stagedBundle, err := i.inspector().Inspect(ctx, stagedPath, true)
	if err != nil {
		return Prepared{}, fmt.Errorf("verify staged update: %w", err)
	}
	if err := i.matchesRelease(stagedBundle, candidate); err != nil {
		return Prepared{}, err
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		i.logger().Warn("could not remove downloaded archive", "path", archivePath, "error", err)
	}
	i.logger().Info("update staged and verified", "harness", i.identity().Name, "build", candidate.Build, "version", candidate.Version, "path", stagedPath)
	return Prepared{Release: candidate, StagedPath: stagedPath}, nil
}

func (i *Installer) Apply(ctx context.Context, prepared Prepared, preflight func(context.Context) error) error {
	if err := i.validate(); err != nil {
		return err
	}
	bundle, err := i.inspector().Inspect(ctx, prepared.StagedPath, true)
	if err != nil {
		return fmt.Errorf("reverify staged update: %w", err)
	}
	if err := i.matchesRelease(bundle, prepared.Release); err != nil {
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

	if err := i.shutdown(ctx); err != nil {
		return err
	}

	backupPath := filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-backup-%s-%d", filepath.Base(i.AppPath), release.Key(current.Build), time.Now().UnixNano()))
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
	i.logger().Info("application update completed", "harness", i.identity().Name, "old_build", current.Build, "new_build", prepared.Release.Build, "version", prepared.Release.Version)
	return nil
}

func (i *Installer) download(ctx context.Context, candidate release.Release, archivePath string) error {
	if info, err := os.Stat(archivePath); err == nil && candidate.Length > 0 && info.Size() == candidate.Length {
		return nil
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove incomplete update archive: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate.URL, nil)
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
	if resp.Request.URL.Scheme != "https" && (resp.Request.URL.Scheme != "http" || !isLoopback(resp.Request.URL.Hostname())) {
		return fmt.Errorf("download update redirected to non-HTTPS URL %s", resp.Request.URL)
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
	const maximumArchiveSize = int64(2 << 30)
	if resp.ContentLength > maximumArchiveSize {
		return fmt.Errorf("downloaded update content length %d exceeds maximum archive size %d", resp.ContentLength, maximumArchiveSize)
	}
	limit := maximumArchiveSize
	if candidate.Length > 0 {
		limit = candidate.Length
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, limit+1))
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("write update archive: %w", copyErr)
	}
	if candidate.Length > 0 && written != candidate.Length {
		return fmt.Errorf("downloaded update length %d does not match advertised length %d", written, candidate.Length)
	}
	if candidate.Length == 0 && written > maximumArchiveSize {
		return fmt.Errorf("downloaded update exceeds maximum archive size %d", maximumArchiveSize)
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
		application, err := i.processes().Application(ctx, i.AppPath, i.identity().Executable)
		if err != nil {
			return err
		}
		if application == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not exit within %s; update aborted", i.identity().Name, i.QuitTimeout)
		}
		if err := sleep(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

func (i *Installer) shutdown(ctx context.Context) error {
	application, err := i.processes().Application(ctx, i.AppPath, i.identity().Executable)
	if err != nil {
		return err
	}
	if application == nil {
		return nil
	}
	i.logger().Info("requesting graceful application shutdown", "harness", i.identity().Name, "pid", application.PID)
	script := fmt.Sprintf(`tell application id %q to quit`, i.identity().BundleIdentifier)
	output, quitErr := i.runner().CombinedOutput(ctx, "/usr/bin/osascript", "-e", script)
	var gracefulCause error
	if quitErr != nil {
		gracefulCause = commandError("request "+i.identity().Name+" quit", output, quitErr)
	} else {
		exitErr := i.waitForExit(ctx)
		if exitErr == nil {
			return nil
		}
		if errors.Is(exitErr, context.Canceled) || errors.Is(exitErr, context.DeadlineExceeded) {
			return exitErr
		}
		gracefulCause = exitErr
	}
	application, err = i.processes().Application(ctx, i.AppPath, i.identity().Executable)
	if err != nil {
		return fmt.Errorf("%w; recheck %s process: %v", gracefulCause, i.identity().Name, err)
	}
	if application == nil {
		return nil
	}
	i.logger().Warn("graceful application shutdown did not stop the application; sending SIGTERM", "harness", i.identity().Name, "pid", application.PID, "error", gracefulCause)
	output, termErr := i.runner().CombinedOutput(ctx, "/bin/kill", "-TERM", strconv.Itoa(application.PID))
	if termErr != nil {
		return fmt.Errorf("%w; %w", gracefulCause, commandError("send SIGTERM to "+i.identity().Name, output, termErr))
	}
	if err := i.waitForExit(ctx); err != nil {
		return fmt.Errorf("%w; application still running after SIGTERM: %v", gracefulCause, err)
	}
	return nil
}

func (i *Installer) launchAndWait(ctx context.Context, expectedBuild string) error {
	output, err := i.runner().CombinedOutput(ctx, "/usr/bin/open", i.AppPath)
	if err != nil {
		return commandError("launch updated "+i.identity().Name, output, err)
	}
	deadline := time.Now().Add(i.LaunchTimeout)
	for {
		application, err := i.processes().Application(ctx, i.AppPath, i.identity().Executable)
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
			return fmt.Errorf("updated %s application did not become ready within %s", i.identity().Name, i.LaunchTimeout)
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
}

func (i *Installer) rollback(ctx context.Context, backupPath string, prepared Prepared, previous macos.Bundle, cause error) error {
	i.logger().Error("updated app failed readiness check; rolling back", "error", cause)
	if err := i.shutdown(ctx); err != nil {
		markerErr := i.markFailure(prepared.Release, cause)
		result := fmt.Errorf("%w; rollback could not stop failed replacement: %v; previous bundle retained at %s", cause, err, backupPath)
		if markerErr != nil {
			result = fmt.Errorf("%w; could not persist quarantine marker: %v", result, markerErr)
		}
		return result
	}
	failedPath := filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-failed-%s-%d", filepath.Base(i.AppPath), release.Key(prepared.Release.Build), time.Now().UnixNano()))
	if err := os.Rename(i.AppPath, failedPath); err != nil {
		return fmt.Errorf("%w; rollback could not move failed app: %v", cause, err)
	}
	if err := os.Rename(backupPath, i.AppPath); err != nil {
		return fmt.Errorf("%w; rollback could not restore previous app: %v", cause, err)
	}
	markerErr := i.markFailure(prepared.Release, cause)
	cleanupErr := removeExact(failedPath, filepath.Dir(i.AppPath))
	relaunchErr := i.launchAndWait(ctx, previous.Build)
	result := fmt.Errorf("%w; previous app restored and version %s quarantined", cause, prepared.Release.Build)
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
	result = fmt.Errorf("%w; version %s quarantined", result, prepared.Release.Build)
	if markerErr != nil {
		result = fmt.Errorf("%w; could not persist quarantine marker: %v", result, markerErr)
	}
	if cleanupErr != nil {
		result = fmt.Errorf("%w; could not remove staged replacement: %v", result, cleanupErr)
	}
	return result
}

func (i *Installer) markFailure(candidate release.Release, cause error) error {
	record := failureRecord{Build: candidate.Build, Version: candidate.Version, FailedAt: time.Now().UTC(), Error: cause.Error()}
	i.rememberFailure(candidate.Build, record.Error)

	finish := func(err error) error {
		if err != nil {
			return err
		}
		i.mu.Lock()
		delete(i.blocked, candidate.Build)
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
	temporaryPath := i.failurePath(candidate.Build) + ".partial-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temporaryPath, append(data, '\n'), 0o600); err != nil {
		return finish(fmt.Errorf("write failure marker: %w", err))
	}
	if err := os.Rename(temporaryPath, i.failurePath(candidate.Build)); err != nil {
		_ = os.Remove(temporaryPath)
		return finish(fmt.Errorf("finish failure marker: %w", err))
	}
	return finish(nil)
}

func (i *Installer) rememberFailure(build string, reason string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.blocked == nil {
		i.blocked = make(map[string]string)
	}
	i.blocked[build] = reason
}

func (i *Installer) failureReason(build string) (string, string, bool) {
	paths := []string{i.failurePath(build)}
	if i.identity().BundleIdentifier == macos.ChatGPTIdentity.BundleIdentifier && !strings.Contains(build, ".") {
		legacyName := "failed-build-" + build + ".json"
		paths = append(paths, filepath.Join(i.CacheDir, legacyName))
		if filepath.Base(i.CacheDir) == "chatgpt" {
			paths = append(paths, filepath.Join(filepath.Dir(i.CacheDir), legacyName))
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err == nil {
			var record struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(data, &record); err != nil || record.Error == "" {
				return "unreadable failure marker", path, true
			}
			return record.Error, path, true
		}
		if !os.IsNotExist(err) {
			return fmt.Sprintf("failure marker cannot be read: %v", err), path, true
		}
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if reason, ok := i.blocked[build]; ok {
		return reason, i.failurePath(build), true
	}
	return "", "", false
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

func (i *Installer) failurePath(build string) string {
	return filepath.Join(i.CacheDir, fmt.Sprintf("failed-version-%s.json", release.Key(build)))
}

func (i *Installer) validate() error {
	if !filepath.IsAbs(i.AppPath) || filepath.Base(i.AppPath) != i.identity().Executable+".app" {
		return fmt.Errorf("app path must be an absolute path ending in %s.app", i.identity().Executable)
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

func (i *Installer) stagedPath(build string) string {
	return filepath.Join(filepath.Dir(i.AppPath), fmt.Sprintf(".%s.codex-autoupdate-%s.new", filepath.Base(i.AppPath), release.Key(build)))
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
	return macos.Inspector{Runner: i.runner(), Identity: i.identity()}
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

func (i *Installer) identity() macos.Identity {
	if i.Identity.BundleIdentifier == "" {
		return macos.ChatGPTIdentity
	}
	return i.Identity
}

func (i *Installer) matchesRelease(bundle macos.Bundle, candidate release.Release) error {
	if bundle.Identifier != i.identity().BundleIdentifier {
		return fmt.Errorf("staged bundle identifier %q does not match %q", bundle.Identifier, i.identity().BundleIdentifier)
	}
	if bundle.TeamID != i.identity().TeamID {
		return fmt.Errorf("staged signing team %q does not match expected team %q", bundle.TeamID, i.identity().TeamID)
	}
	if bundle.Build != candidate.Build {
		return fmt.Errorf("staged build %s does not match advertised build %s", bundle.Build, candidate.Build)
	}
	if candidate.Version != "" && bundle.Version != candidate.Version {
		return fmt.Errorf("staged version %q does not match advertised version %q", bundle.Version, candidate.Version)
	}
	return nil
}

func findExtractedApp(root, appName string) (string, error) {
	direct := filepath.Join(root, appName)
	if info, err := os.Lstat(direct); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("update archive %s is not a real application bundle directory", appName)
		}
		return direct, nil
	}
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == appName {
			if found != "" {
				return fmt.Errorf("update archive contains multiple %s bundles", appName)
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
		return "", fmt.Errorf("update archive does not contain %s", appName)
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

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
