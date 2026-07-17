package launchagent

import (
	"strings"
	"testing"
)

func TestRenderPlistPreservesArgumentsAndEscapesXML(t *testing.T) {
	t.Parallel()
	content, err := renderPlist(Config{
		Executable:           "/tmp/a&b/codex-autoupdate",
		AppPath:              "/Applications/ChatGPT.app",
		CodexHome:            "/tmp/.codex",
		CacheDir:             "/tmp/cache",
		FeedURL:              "https://example.test/appcast.xml?a=1&b=2",
		IdleWindow:           "5m0s",
		PollInterval:         "15m0s",
		ActivityPollInterval: "5s",
		QuitTimeout:          "30s",
		LaunchTimeout:        "1m30s",
	}, "/tmp/out.log", "/tmp/err.log")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{"/tmp/a&amp;b/codex-autoupdate", "appcast.xml?a=1&amp;b=2", "--idle-window", "5m0s", "<key>KeepAlive</key>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("plist missing %q:\n%s", expected, text)
		}
	}
}
