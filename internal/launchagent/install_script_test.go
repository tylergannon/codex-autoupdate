package launchagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScriptAlwaysInstallsLatestAndForwardsFlags(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	content, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	functions, err := installerFunctionBody(string(content))
	if err != nil {
		t.Fatal(err)
	}
	functionsPath := filepath.Join(root, "installer-functions.sh")
	if err := os.WriteFile(functionsPath, []byte(functions), 0o600); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(fakeBin, "uname"), "#!/bin/bash\nprintf 'Darwin\\n'\n")
	writeExecutable(t, filepath.Join(fakeBin, "id"), `#!/bin/bash
case "$1" in
  -Gn) printf 'staff admin\n' ;;
  -un) printf 'fixture-user\n' ;;
  *) exit 2 ;;
esac
`)
	writeExecutable(t, filepath.Join(fakeBin, "go"), `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >"$INSTALL_TEST_LOG/go.log"
/bin/mkdir -p "$GOBIN"
/bin/cp "$FAKE_INSTALLED_BINARY" "$GOBIN/codex-autoupdate"
/bin/chmod 0755 "$GOBIN/codex-autoupdate"
`)
	fakeInstalled := filepath.Join(root, "fake-codex-autoupdate")
	writeExecutable(t, fakeInstalled, `#!/bin/bash
printf '%s\n' "$*" >>"$INSTALL_TEST_LOG/binary.log"
if [ "$*" = "--version" ]; then
  printf 'codex-autoupdate version v9.9.9\n'
fi
`)

	home := filepath.Join(root, "home")
	appPath := filepath.Join(root, "Applications", "ChatGPT.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(root, "harness.sh")
	harnessContent := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
source %q
resolve_user_home() { printf '%%s\n' "$TEST_HOME"; }
main --app-path /missing/ChatGPT.app --app-path=%q --idle-window 10m --poll-interval=30m
`, functionsPath, appPath)
	writeExecutable(t, harness, harnessContent)

	command := exec.Command("/bin/bash", harness)
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+":/usr/bin:/bin",
		"TEST_HOME="+home,
		"INSTALL_TEST_LOG="+root,
		"FAKE_INSTALLED_BINARY="+fakeInstalled,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installer harness failed: %v\n%s", err, output)
	}
	installed := filepath.Join(home, "Library", "Application Support", "codex-autoupdate", "codex-autoupdate")
	if info, err := os.Stat(installed); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("canonical binary missing or not executable: %v", err)
	}
	goLog, err := os.ReadFile(filepath.Join(root, "go.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(goLog) != "install github.com/tylergannon/codex-autoupdate/cmd/codex-autoupdate@latest\n" {
		t.Fatalf("unexpected go invocation: %s", goLog)
	}
	if strings.Contains(string(content), "CODEX_AUTOUPDATE_VERSION") {
		t.Fatal("installer must not expose a version override")
	}
	binaryLog, err := os.ReadFile(filepath.Join(root, "binary.log"))
	if err != nil {
		t.Fatal(err)
	}
	wantInstall := "--app-path /missing/ChatGPT.app --app-path=" + appPath + " --idle-window 10m --poll-interval=30m install"
	if lines := strings.Split(strings.TrimSpace(string(binaryLog)), "\n"); len(lines) != 2 || lines[0] != wantInstall || lines[1] != "--version" {
		t.Fatalf("unexpected installed-binary calls: %q", lines)
	}
	if !strings.Contains(string(output), "codex-autoupdate version v9.9.9") || !strings.Contains(string(output), installed) {
		t.Fatalf("installer output omitted version or path:\n%s", output)
	}
}

func TestInstallerFunctionBodyFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := installerFunctionBody("main --unexpected\n"); err == nil {
		t.Fatal("expected changed final invocation to be rejected")
	}
}

func TestInstallScriptRejectsMissingAppBeforeGoInstall(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	content, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	functions, err := installerFunctionBody(string(content))
	if err != nil {
		t.Fatal(err)
	}
	functionsPath := filepath.Join(root, "installer-functions.sh")
	if err := os.WriteFile(functionsPath, []byte(functions), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(fakeBin, "uname"), "#!/bin/bash\nprintf 'Darwin\\n'\n")
	writeExecutable(t, filepath.Join(fakeBin, "id"), "#!/bin/bash\nprintf 'staff admin\\n'\n")
	writeExecutable(t, filepath.Join(fakeBin, "go"), "#!/bin/bash\ntouch \"$INSTALL_TEST_LOG/go-ran\"\n")
	harness := filepath.Join(root, "harness.sh")
	writeExecutable(t, harness, fmt.Sprintf("#!/bin/bash\nset -euo pipefail\nsource %q\nresolve_user_home() { printf '%%s\\n' /unused; }\nmain --app-path %q\n", functionsPath, filepath.Join(root, "missing", "ChatGPT.app")))

	command := exec.Command("/bin/bash", harness)
	command.Env = append(os.Environ(), "PATH="+fakeBin+":/usr/bin:/bin", "INSTALL_TEST_LOG="+root)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "ChatGPT Desktop is not installed") {
		t.Fatalf("expected missing-app preflight failure, got %v:\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "go-ran")); !os.IsNotExist(err) {
		t.Fatalf("go install ran after failed preflight: %v", err)
	}
}

func installerFunctionBody(content string) (string, error) {
	const invocation = `main "$@"`
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if len(lines) == 0 || lines[len(lines)-1] != invocation {
		return "", fmt.Errorf("installer final line must be exactly %s", invocation)
	}
	return strings.Join(lines[:len(lines)-1], "\n") + "\n", nil
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
