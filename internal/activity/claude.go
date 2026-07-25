package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/macos"
)

type ClaudeProcessSource interface {
	All(ctx context.Context) ([]macos.Process, error)
	Application(ctx context.Context, appPath, executableName string) (*macos.Process, error)
	OpenFilesUnder(ctx context.Context, root string) (map[int]struct{}, error)
}

type ClaudeDetector struct {
	AppPath    string
	ClaudeData string
	TaskRoot   string
	Processes  ClaudeProcessSource
}

func (d ClaudeDetector) Detect(ctx context.Context) (Report, error) {
	processes := d.Processes
	if processes == nil {
		finder := macos.ProcessFinder{}
		processes = finder
	}
	application, err := processes.Application(ctx, d.AppPath, macos.ClaudeIdentity.Executable)
	if err != nil {
		return Report{}, err
	}
	all, err := processes.All(ctx)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	if application != nil {
		report.AppServerPID = application.PID
		report.AppServerStart = application.Started
		report.LastLifecycle = application.Started
	}
	for _, process := range all {
		if application != nil && process.PID == application.PID {
			continue
		}
		if strings.HasPrefix(process.Command, filepath.Clean(d.AppPath)+string(filepath.Separator)) {
			continue
		}
		if !isClaudeCodeProcess(process.Command) {
			continue
		}
		report.ActiveThreads = append(report.ActiveThreads, fmt.Sprintf("claude-code-pid:%d", process.PID))
		if process.Started.After(report.LastLifecycle) {
			report.LastLifecycle = process.Started
		}
	}
	taskRoot := d.TaskRoot
	if taskRoot == "" {
		taskRoot = filepath.Join("/private/tmp", fmt.Sprintf("claude-%d", os.Getuid()))
	}
	taskPIDs, err := processes.OpenFilesUnder(ctx, taskRoot)
	if err != nil {
		return Report{}, err
	}
	for _, process := range all {
		if _, active := taskPIDs[process.PID]; !active {
			continue
		}
		report.ActiveThreads = append(report.ActiveThreads, fmt.Sprintf("claude-task-pid:%d", process.PID))
		if process.Started.After(report.LastLifecycle) {
			report.LastLifecycle = process.Started
		}
	}
	roots := []string{
		filepath.Join(d.ClaudeData, "claude-code-sessions"),
		filepath.Join(d.ClaudeData, "local-agent-mode-sessions"),
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "local_") || !strings.HasSuffix(entry.Name(), ".json") {
				return nil
			}
			record, err := readClaudeSession(path)
			if err != nil {
				return fmt.Errorf("read Claude session %s: %w", path, err)
			}
			if record.IsArchived {
				return nil
			}
			active := sessionProcessRunning(all, record.identifiers())
			if !active && application == nil {
				return nil
			}
			if !active && record.LastActivity.Before(application.Started.Add(-10*time.Second)) {
				return nil
			}
			if record.LastActivity.After(report.LastLifecycle) {
				report.LastLifecycle = record.LastActivity
			}
			if active {
				report.ActiveThreads = append(report.ActiveThreads, record.SessionID)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return Report{}, err
		}
	}
	slices.Sort(report.ActiveThreads)
	report.ActiveThreads = slices.Compact(report.ActiveThreads)
	return report, nil
}

func isClaudeCodeProcess(command string) bool {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, "/") && strings.Contains(command, "/Claude.app/Contents/") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	executable := filepath.Clean(fields[0])
	base := strings.ToLower(filepath.Base(executable))
	if base == "claude" || base == "claude-code" {
		return true
	}
	if strings.Contains(executable, "/.local/share/claude/versions/") {
		return true
	}
	for _, field := range fields[1:] {
		cleaned := filepath.Clean(field)
		if strings.Contains(cleaned, "/@anthropic-ai/claude-code/") {
			return true
		}
	}
	return false
}

type claudeSession struct {
	SessionID    string `json:"sessionId"`
	CLISessionID string `json:"cliSessionId"`
	ProcessName  string `json:"processName"`
	VMProcess    string `json:"vmProcessName"`
	IsArchived   bool   `json:"isArchived"`
	LastActivity time.Time
}

func readClaudeSession(path string) (claudeSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return claudeSession{}, err
	}
	var raw struct {
		SessionID      string `json:"sessionId"`
		CLISessionID   string `json:"cliSessionId"`
		ProcessName    string `json:"processName"`
		VMProcess      string `json:"vmProcessName"`
		IsArchived     bool   `json:"isArchived"`
		LastActivityAt int64  `json:"lastActivityAt"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return claudeSession{}, err
	}
	if raw.SessionID == "" {
		return claudeSession{}, fmt.Errorf("missing sessionId")
	}
	lastActivity := time.UnixMilli(raw.LastActivityAt)
	if raw.LastActivityAt <= 0 {
		info, err := os.Stat(path)
		if err != nil {
			return claudeSession{}, err
		}
		lastActivity = info.ModTime()
	}
	return claudeSession{
		SessionID:    raw.SessionID,
		CLISessionID: raw.CLISessionID,
		ProcessName:  raw.ProcessName,
		VMProcess:    raw.VMProcess,
		IsArchived:   raw.IsArchived,
		LastActivity: lastActivity,
	}, nil
}

func (s claudeSession) identifiers() []string {
	var result []string
	for _, value := range []string{s.SessionID, s.CLISessionID, s.ProcessName, s.VMProcess} {
		value = strings.TrimSpace(value)
		if value != "" && len(value) >= 8 {
			result = append(result, value)
		}
	}
	return slices.Compact(result)
}

func sessionProcessRunning(processes []macos.Process, identifiers []string) bool {
	for _, process := range processes {
		for _, identifier := range identifiers {
			if strings.Contains(process.Command, identifier) {
				return true
			}
		}
	}
	return false
}
