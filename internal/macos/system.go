package macos

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	BundleIdentifier = "com.openai.codex"
	OpenAITeamID     = "2DC432GLL2"
)

type Runner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Bundle struct {
	Path       string
	Identifier string
	Version    string
	Build      int64
	TeamID     string
}

type Inspector struct {
	Runner Runner
}

func (i Inspector) Inspect(ctx context.Context, appPath string, verify bool) (Bundle, error) {
	runner := i.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	infoPath := filepath.Join(appPath, "Contents", "Info.plist")
	identifier, err := plistValue(ctx, runner, infoPath, "CFBundleIdentifier")
	if err != nil {
		return Bundle{}, err
	}
	version, err := plistValue(ctx, runner, infoPath, "CFBundleShortVersionString")
	if err != nil {
		return Bundle{}, err
	}
	buildText, err := plistValue(ctx, runner, infoPath, "CFBundleVersion")
	if err != nil {
		return Bundle{}, err
	}
	build, err := strconv.ParseInt(buildText, 10, 64)
	if err != nil || build <= 0 {
		return Bundle{}, fmt.Errorf("invalid CFBundleVersion %q in %s", buildText, infoPath)
	}

	bundle := Bundle{Path: appPath, Identifier: identifier, Version: version, Build: build}
	if !verify {
		return bundle, nil
	}
	if identifier != BundleIdentifier {
		return Bundle{}, fmt.Errorf("unexpected bundle identifier %q in %s", identifier, appPath)
	}
	if output, err := runner.CombinedOutput(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "--verbose=2", appPath); err != nil {
		return Bundle{}, commandError("verify code signature", output, err)
	}
	output, err := runner.CombinedOutput(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", appPath)
	if err != nil {
		return Bundle{}, commandError("read code signature", output, err)
	}
	bundle.TeamID = signatureValue(string(output), "TeamIdentifier")
	if bundle.TeamID != OpenAITeamID {
		return Bundle{}, fmt.Errorf("unexpected signing team %q for %s", bundle.TeamID, appPath)
	}
	if output, err := runner.CombinedOutput(ctx, "/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=2", appPath); err != nil {
		return Bundle{}, commandError("assess app with Gatekeeper", output, err)
	}
	executable := filepath.Join(appPath, "Contents", "MacOS", "ChatGPT")
	output, err = runner.CombinedOutput(ctx, "/usr/bin/lipo", "-archs", executable)
	if err != nil {
		return Bundle{}, commandError("inspect executable architecture", output, err)
	}
	wantedArch := runtime.GOARCH
	if wantedArch == "amd64" {
		wantedArch = "x86_64"
	}
	if !slices.Contains(strings.Fields(string(output)), wantedArch) {
		return Bundle{}, fmt.Errorf("app executable does not contain host architecture %s", wantedArch)
	}
	return bundle, nil
}

func plistValue(ctx context.Context, runner Runner, plistPath, key string) (string, error) {
	output, err := runner.CombinedOutput(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, plistPath)
	if err != nil {
		return "", commandError("read "+key, output, err)
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("empty %s in %s", key, plistPath)
	}
	return value, nil
}

func signatureValue(output, key string) string {
	prefix := key + "="
	for line := range strings.Lines(output) {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

type Process struct {
	PID     int
	Started time.Time
	Command string
}

type ProcessFinder struct {
	Runner    Runner
	CodexHome string
}

func (f ProcessFinder) All(ctx context.Context) ([]Process, error) {
	output, err := f.runner().CombinedOutput(ctx, "/bin/ps", "-axo", "pid=,lstart=,command=")
	if err != nil {
		return nil, commandError("list processes", output, err)
	}
	var processes []Process
	for line := range strings.Lines(string(output)) {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		started, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(fields[1:6], " "), time.Local)
		if err != nil {
			continue
		}
		processes = append(processes, Process{PID: pid, Started: started, Command: strings.Join(fields[6:], " ")})
	}
	return processes, nil
}

func (f ProcessFinder) DesktopAppServer(ctx context.Context, appPath string) (*Process, error) {
	processes, err := f.All(ctx)
	if err != nil {
		return nil, err
	}
	holders, checked, err := f.controlSocketHolders(ctx)
	if err != nil {
		return nil, err
	}
	if checked && len(holders) > 0 {
		return newestMatching(processes, func(process Process) bool {
			_, holdsSocket := holders[process.PID]
			return holdsSocket && commandHasArgument(process.Command, "app-server")
		}), nil
	}

	executable := filepath.Join(appPath, "Contents", "Resources", "codex")
	return newestMatching(processes, func(process Process) bool {
		return strings.HasPrefix(process.Command, executable+" ") && commandHasArgument(process.Command, "app-server")
	}), nil
}

func (f ProcessFinder) DesktopApplication(ctx context.Context, appPath string) (*Process, error) {
	processes, err := f.All(ctx)
	if err != nil {
		return nil, err
	}
	executable := filepath.Join(filepath.Clean(appPath), "Contents", "MacOS", "ChatGPT")
	return newestMatching(processes, func(process Process) bool {
		return process.Command == executable || strings.HasPrefix(process.Command, executable+" ")
	}), nil
}

func (f ProcessFinder) controlSocketHolders(ctx context.Context) (map[int]struct{}, bool, error) {
	if f.CodexHome == "" {
		return nil, false, nil
	}
	socketPath := filepath.Join(f.CodexHome, "app-server-control", "app-server-control.sock")
	output, err := f.runner().CombinedOutput(ctx, "/usr/sbin/lsof", "-n", "-t", socketPath)
	if err != nil {
		if strings.TrimSpace(string(output)) == "" {
			return nil, true, nil
		}
		return nil, true, commandError("find Desktop app-server control socket owner", output, err)
	}
	holders := make(map[int]struct{})
	for line := range strings.Lines(string(output)) {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		pid, err := strconv.Atoi(value)
		if err != nil {
			return nil, true, fmt.Errorf("invalid Desktop app-server PID %q from lsof", value)
		}
		holders[pid] = struct{}{}
	}
	return holders, true, nil
}

func (f ProcessFinder) runner() Runner {
	if f.Runner != nil {
		return f.Runner
	}
	return ExecRunner{}
}

func newestMatching(processes []Process, matches func(Process) bool) *Process {
	var newest *Process
	for _, process := range processes {
		if !matches(process) || newest != nil && !process.Started.After(newest.Started) {
			continue
		}
		copy := process
		newest = &copy
	}
	return newest
}

func commandHasArgument(command, argument string) bool {
	return slices.Contains(strings.Fields(command), argument)
}

func commandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}
