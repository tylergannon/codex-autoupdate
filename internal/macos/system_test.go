package macos

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type outputRunner string

func (r outputRunner) CombinedOutput(context.Context, string, ...string) ([]byte, error) {
	return []byte(r), nil
}

func TestProcessFinderFindsNewestDesktopAppServer(t *testing.T) {
	t.Parallel()
	appPath := "/Applications/ChatGPT.app"
	output := fmt.Sprintf(` 11 Fri Jul 17 09:00:00 2026 %s/Contents/Resources/codex app-server
 12 Fri Jul 17 10:00:00 2026 %s/Contents/Resources/codex -c key=true app-server --analytics-default-enabled
 13 Fri Jul 17 11:00:00 2026 /usr/local/bin/codex app-server
`, appPath, appPath)
	finder := ProcessFinder{Runner: outputRunner(output)}
	server, err := finder.DesktopAppServer(context.Background(), appPath)
	if err != nil {
		t.Fatal(err)
	}
	if server == nil || server.PID != 12 {
		t.Fatalf("unexpected server: %+v", server)
	}
	want, _ := time.ParseInLocation("Mon Jan 2 15:04:05 2006", "Fri Jul 17 10:00:00 2026", time.Local)
	if !server.Started.Equal(want) {
		t.Fatalf("start time %s, want %s", server.Started, want)
	}
}

func TestBundleProcessesMatchOnlyConfiguredBundlePrefix(t *testing.T) {
	t.Parallel()
	output := ` 11 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT
 12 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT Classic.app/Contents/MacOS/ChatGPT
`
	processes, err := (ProcessFinder{Runner: outputRunner(output)}).BundleProcesses(context.Background(), "/Applications/ChatGPT.app")
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || processes[0].PID != 11 {
		t.Fatalf("unexpected processes: %+v", processes)
	}
}
