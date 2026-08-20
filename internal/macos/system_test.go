package macos

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type outputRunner string

func (r outputRunner) CombinedOutput(context.Context, string, ...string) ([]byte, error) {
	return []byte(r), nil
}

type commandRunner map[string]string

func (r commandRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	output, ok := r[key]
	if !ok {
		return nil, fmt.Errorf("unexpected command: %s", key)
	}
	return []byte(output), nil
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

func TestProcessFinderFindsControlSocketAppServer(t *testing.T) {
	t.Parallel()
	appPath := "/Applications/ChatGPT.app"
	codexHome := "/Users/test/.codex"
	processes := ` 11 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT.app/Contents/Resources/codex app-server
 12 Fri Jul 17 10:00:00 2026 codex -c features.code_mode_host=true app-server --listen unix://
 13 Fri Jul 17 11:00:00 2026 codex app-server proxy
`
	runner := commandRunner{
		"/bin/ps -axo pid=,lstart=,command=":                                                processes,
		"/usr/sbin/lsof -n -t " + codexHome + "/app-server-control/app-server-control.sock": "12\n",
	}
	server, err := (ProcessFinder{Runner: runner, CodexHome: codexHome}).DesktopAppServer(context.Background(), appPath)
	if err != nil {
		t.Fatal(err)
	}
	if server == nil || server.PID != 12 {
		t.Fatalf("unexpected server: %+v", server)
	}
}

func TestProcessFinderFallsBackWhenControlSocketHasNoOwner(t *testing.T) {
	t.Parallel()
	appPath := "/Applications/ChatGPT.app"
	codexHome := "/Users/test/.codex"
	runner := commandRunner{
		"/bin/ps -axo pid=,lstart=,command=":                                                " 11 Fri Jul 17 09:00:00 2026 " + appPath + "/Contents/Resources/codex app-server\n",
		"/usr/sbin/lsof -n -t " + codexHome + "/app-server-control/app-server-control.sock": "",
	}
	server, err := (ProcessFinder{Runner: runner, CodexHome: codexHome}).DesktopAppServer(context.Background(), appPath)
	if err != nil {
		t.Fatal(err)
	}
	if server == nil || server.PID != 11 {
		t.Fatalf("unexpected server: %+v", server)
	}
}

func TestDesktopApplicationExcludesPersistentBundleHelpers(t *testing.T) {
	t.Parallel()
	output := ` 11 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT
	12 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT.app/Contents/Frameworks/Codex Framework.framework/Helpers/browser_crashpad_handler
	13 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT.app/Contents/Resources/cua_node/bin/node_repl
	14 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT Classic.app/Contents/MacOS/ChatGPT
`
	application, err := (ProcessFinder{Runner: outputRunner(output)}).DesktopApplication(context.Background(), "/Applications/ChatGPT.app")
	if err != nil {
		t.Fatal(err)
	}
	if application == nil || application.PID != 11 {
		t.Fatalf("unexpected application: %+v", application)
	}
}

func TestProcessFinderListsEveryBundleOwnedProcess(t *testing.T) {
	t.Parallel()
	appPath := "/Applications/Claude.app"
	output := ` 11 Fri Jul 17 09:00:00 2026 /Applications/Claude.app/Contents/MacOS/Claude
	12 Fri Jul 17 09:00:01 2026 /Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper --type=gpu-process
	13 Fri Jul 17 09:00:02 2026 /Applications/Claude.app/Contents/Resources/native/detached-helper
	14 Fri Jul 17 09:00:03 2026 /Applications/Claude Classic.app/Contents/MacOS/Claude
`
	processes, err := (ProcessFinder{Runner: outputRunner(output)}).BundleProcesses(context.Background(), appPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 3 {
		t.Fatalf("bundle processes = %+v, want PIDs 11, 12, and 13", processes)
	}
	if got := []int{processes[0].PID, processes[1].PID, processes[2].PID}; fmt.Sprint(got) != "[11 12 13]" {
		t.Fatalf("bundle process PIDs = %v, want [11 12 13]", got)
	}
}

func TestProcessFinderListsAllMainApplicationInstances(t *testing.T) {
	t.Parallel()
	output := ` 11 Fri Jul 17 09:00:00 2026 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT
	12 Fri Jul 17 10:00:00 2026 /Applications/ChatGPT.app/Contents/MacOS/ChatGPT --duplicate
`
	applications, err := (ProcessFinder{Runner: outputRunner(output)}).Applications(context.Background(), "/Applications/ChatGPT.app", "ChatGPT")
	if err != nil {
		t.Fatal(err)
	}
	if len(applications) != 2 || applications[0].PID != 11 || applications[1].PID != 12 {
		t.Fatalf("applications = %+v, want both main processes", applications)
	}
}

func TestOpenFilesUnderFindsDeletedClaudeTaskOutputs(t *testing.T) {
	t.Parallel()
	runner := commandRunner{
		"/usr/sbin/lsof -n -Fpn -d0,1,2": "p20\nf1\nn/private/tmp/claude-501/session/tasks/task.output\np21\nf1\nn/private/tmp/other.output\n",
	}
	processes, err := (ProcessFinder{Runner: runner}).OpenFilesUnder(context.Background(), "/private/tmp/claude-501")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := processes[20]; !ok || len(processes) != 1 {
		t.Fatalf("processes = %v, want only PID 20", processes)
	}
}
