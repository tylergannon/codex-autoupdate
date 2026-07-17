package launchagent

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

const Label = "com.tylergannon.codex-autoupdate"

type Config struct {
	Executable           string
	AppPath              string
	CodexHome            string
	CacheDir             string
	FeedURL              string
	IdleWindow           string
	PollInterval         string
	ActivityPollInterval string
	QuitTimeout          string
	LaunchTimeout        string
}

type Manager struct {
	HomeDir      string
	UID          int
	Runner       Runner
	RequireAdmin func() error
}

type Runner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (m Manager) Install(ctx context.Context, config Config) error {
	if err := m.requireAdmin(); err != nil {
		return err
	}
	paths, err := m.paths()
	if err != nil {
		return err
	}
	if err := validateApp(config.AppPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.stdout), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	if err := validateCanonicalExecutable(config.Executable, paths.binary); err != nil {
		return err
	}
	config.Executable = paths.binary
	plist, err := renderPlist(config, paths.stdout, paths.stderr)
	if err != nil {
		return err
	}
	if err := writeAtomic(paths.plist, plist, 0o644); err != nil {
		return fmt.Errorf("write LaunchAgent plist: %w", err)
	}
	domain := "gui/" + strconv.Itoa(paths.uid)
	target := domain + "/" + Label
	_, _ = m.runner().CombinedOutput(ctx, "/bin/launchctl", "bootout", target)
	if output, err := m.runner().CombinedOutput(ctx, "/bin/launchctl", "bootstrap", domain, paths.plist); err != nil {
		return commandError("bootstrap LaunchAgent", output, err)
	}
	if output, err := m.runner().CombinedOutput(ctx, "/bin/launchctl", "kickstart", target); err != nil {
		return commandError("start LaunchAgent", output, err)
	}
	return nil
}

func (m Manager) Uninstall(ctx context.Context) error {
	paths, err := m.paths()
	if err != nil {
		return err
	}
	target := "gui/" + strconv.Itoa(paths.uid) + "/" + Label
	_, _ = m.runner().CombinedOutput(ctx, "/bin/launchctl", "bootout", target)
	if err := os.Remove(paths.plist); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove LaunchAgent plist: %w", err)
	}
	if err := os.Remove(paths.binary); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove installed executable: %w", err)
	}
	return nil
}

func (m Manager) Status(ctx context.Context) (string, error) {
	paths, err := m.paths()
	if err != nil {
		return "", err
	}
	target := "gui/" + strconv.Itoa(paths.uid) + "/" + Label
	output, err := m.runner().CombinedOutput(ctx, "/bin/launchctl", "print", target)
	if err != nil {
		return "", commandError("read LaunchAgent status", output, err)
	}
	return string(output), nil
}

type installPaths struct {
	uid    int
	binary string
	plist  string
	stdout string
	stderr string
}

func (m Manager) paths() (installPaths, error) {
	homeDir := m.HomeDir
	uid := m.UID
	if homeDir == "" || uid == 0 {
		current, err := user.Current()
		if err != nil {
			return installPaths{}, fmt.Errorf("resolve current user: %w", err)
		}
		if homeDir == "" {
			homeDir = current.HomeDir
		}
		if uid == 0 {
			uid, err = strconv.Atoi(current.Uid)
			if err != nil {
				return installPaths{}, fmt.Errorf("parse current user ID: %w", err)
			}
		}
	}
	return installPaths{
		uid:    uid,
		binary: filepath.Join(homeDir, "Library", "Application Support", "codex-autoupdate", "codex-autoupdate"),
		plist:  filepath.Join(homeDir, "Library", "LaunchAgents", Label+".plist"),
		stdout: filepath.Join(homeDir, "Library", "Logs", "codex-autoupdate", "stdout.log"),
		stderr: filepath.Join(homeDir, "Library", "Logs", "codex-autoupdate", "stderr.log"),
	}, nil
}

func requireAdminUser() error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current user: %w", err)
	}
	admin, err := user.LookupGroup("admin")
	if err != nil {
		return fmt.Errorf("look up macOS admin group: %w", err)
	}
	groups, err := current.GroupIds()
	if err != nil {
		return fmt.Errorf("read current user groups: %w", err)
	}
	if !slices.Contains(groups, admin.Gid) {
		return fmt.Errorf("LaunchAgent installation requires a macOS administrator account")
	}
	return nil
}

func (m Manager) requireAdmin() error {
	if m.RequireAdmin != nil {
		return m.RequireAdmin()
	}
	return requireAdminUser()
}

func (m Manager) runner() Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return execRunner{}
}

func validateApp(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("ChatGPT Desktop is not installed at %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ChatGPT Desktop path is not an application bundle directory: %s", path)
	}
	return nil
}

func validateCanonicalExecutable(actual, expected string) error {
	actualInfo, err := os.Stat(actual)
	if err != nil {
		return fmt.Errorf("inspect current executable: %w", err)
	}
	expectedInfo, err := os.Stat(expected)
	if err != nil {
		return fmt.Errorf("codex-autoupdate must be installed at %s; follow https://github.com/tylergannon/codex-autoupdate/blob/main/llms.txt: %w", expected, err)
	}
	if !os.SameFile(actualInfo, expectedInfo) {
		return fmt.Errorf("codex-autoupdate is running from %s, but the LaunchAgent requires %s; follow https://github.com/tylergannon/codex-autoupdate/blob/main/llms.txt", actual, expected)
	}
	if !actualInfo.Mode().IsRegular() || actualInfo.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("installed executable is not a runnable regular file: %s", expected)
	}
	return nil
}

func writeAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".launchagent-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func renderPlist(config Config, stdout, stderr string) ([]byte, error) {
	arguments := []string{
		config.Executable,
		"run",
		"--app-path", config.AppPath,
		"--codex-home", config.CodexHome,
		"--cache-dir", config.CacheDir,
		"--feed-url", config.FeedURL,
		"--idle-window", config.IdleWindow,
		"--poll-interval", config.PollInterval,
		"--activity-poll-interval", config.ActivityPollInterval,
		"--quit-timeout", config.QuitTimeout,
		"--launch-timeout", config.LaunchTimeout,
	}
	data := struct {
		Arguments []string
		Stdout    string
		Stderr    string
	}{arguments, stdout, stderr}
	tmpl, err := template.New("plist").Funcs(template.FuncMap{"xml": xmlEscape}).Parse(plistTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse LaunchAgent template: %w", err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, data); err != nil {
		return nil, fmt.Errorf("render LaunchAgent template: %w", err)
	}
	return output.Bytes(), nil
}

func xmlEscape(value string) string {
	var output bytes.Buffer
	_ = xml.EscapeText(&output, []byte(value))
	return output.String()
}

func commandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + Label + `</string>
  <key>ProgramArguments</key>
  <array>{{range .Arguments}}
    <string>{{xml .}}</string>{{end}}
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>60</integer>
  <key>StandardOutPath</key>
  <string>{{xml .Stdout}}</string>
  <key>StandardErrorPath</key>
  <string>{{xml .Stderr}}</string>
</dict>
</plist>
`
