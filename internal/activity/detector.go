package activity

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tylergannon/codex-autoupdate/internal/macos"
)

const desktopOriginator = "Codex Desktop"

type Report struct {
	AppServerPID   int
	AppServerStart time.Time
	ActiveThreads  []string
	LastLifecycle  time.Time
}

func (r Report) Active() bool {
	return len(r.ActiveThreads) > 0
}

type ProcessSource interface {
	DesktopAppServer(ctx context.Context, appPath string) (*macos.Process, error)
}

type Detector struct {
	AppPath   string
	CodexHome string
	Processes ProcessSource

	mu        sync.Mutex
	serverPID int
	cache     map[string]cachedRollout
}

type cachedRollout struct {
	modTime time.Time
	size    int64
	state   rolloutState
}

func (d *Detector) Detect(ctx context.Context) (Report, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	processes := d.Processes
	if processes == nil {
		processes = macos.ProcessFinder{}
	}
	server, err := processes.DesktopAppServer(ctx, d.AppPath)
	if err != nil {
		return Report{}, err
	}
	if server == nil {
		d.serverPID = 0
		d.cache = nil
		return Report{}, nil
	}
	if d.serverPID != server.PID {
		d.serverPID = server.PID
		d.cache = make(map[string]cachedRollout)
	}
	report := Report{AppServerPID: server.PID, AppServerStart: server.Started, LastLifecycle: server.Started}
	cutoff := server.Started.Add(-10 * time.Second)
	for _, root := range []string{filepath.Join(d.CodexHome, "sessions"), filepath.Join(d.CodexHome, "archived_sessions")} {
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
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.ModTime().Before(cutoff) {
				return nil
			}
			cached, ok := d.cache[path]
			state := cached.state
			if !ok || !cached.modTime.Equal(info.ModTime()) || cached.size != info.Size() {
				state, err = readRollout(path)
				if err != nil {
					return fmt.Errorf("read rollout %s: %w", path, err)
				}
				d.cache[path] = cachedRollout{modTime: info.ModTime(), size: info.Size(), state: state}
			}
			if state.originator != desktopOriginator || state.lastLifecycle.Before(cutoff) {
				return nil
			}
			if state.lastLifecycle.After(report.LastLifecycle) {
				report.LastLifecycle = state.lastLifecycle
			}
			if state.active {
				report.ActiveThreads = append(report.ActiveThreads, state.threadID)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return Report{}, err
		}
	}
	slices.Sort(report.ActiveThreads)
	return report, nil
}

type rolloutState struct {
	originator    string
	threadID      string
	active        bool
	lastLifecycle time.Time
}

type rolloutRecord struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	Originator string `json:"originator"`
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
}

type eventMessage struct {
	Type string `json:"type"`
}

func readRollout(path string) (rolloutState, error) {
	file, err := os.Open(path)
	if err != nil {
		return rolloutState{}, err
	}
	defer func() { _ = file.Close() }()

	var state rolloutState
	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 16<<20 {
			return rolloutState{}, fmt.Errorf("JSONL record exceeds 16 MiB")
		}
		if len(line) == 0 && readErr == io.EOF {
			break
		}
		var record rolloutRecord
		if err := json.Unmarshal(line, &record); err != nil {
			if readErr == io.EOF {
				break
			}
			return rolloutState{}, fmt.Errorf("decode JSONL record: %w", err)
		}
		switch record.Type {
		case "session_meta":
			var meta sessionMeta
			if err := json.Unmarshal(record.Payload, &meta); err != nil {
				return rolloutState{}, fmt.Errorf("decode session metadata: %w", err)
			}
			if meta.Originator != "" {
				state.originator = meta.Originator
			}
			if meta.ID != "" {
				state.threadID = meta.ID
			} else if meta.SessionID != "" {
				state.threadID = meta.SessionID
			}
		case "event_msg":
			var event eventMessage
			if err := json.Unmarshal(record.Payload, &event); err != nil {
				return rolloutState{}, fmt.Errorf("decode event message: %w", err)
			}
			switch event.Type {
			case "task_started":
				state.active = true
				state.lastLifecycle = record.Timestamp
			case "task_complete", "turn_aborted":
				state.active = false
				state.lastLifecycle = record.Timestamp
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return rolloutState{}, readErr
		}
	}
	if state.threadID == "" {
		state.threadID = filepath.Base(path)
	}
	return state, nil
}
