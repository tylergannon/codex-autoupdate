# Installer proof

Proved implementation checkpoint: `b9f6539` (`feat: add agent-first installer`).

## Claims demonstrated before release

### The bootstrap installs exactly one canonical binary and forwards configuration

`TestInstallScriptInstallsCanonicalBinaryAndForwardsFlags` executes the real `install.sh` function body with a fake Go command and installed CLI in an isolated directory-services home. It demonstrates that `go install` targets `~/Library/Application Support/codex-autoupdate`, the canonical executable is invoked directly, repeated `--app-path` follows Cobra's last-value behavior, and non-default idle/poll flags reach the CLI registration call. The harness verifies the production script's final line before removing it, so a changed invocation fails closed instead of running against the real home.

### LaunchAgent registration refreshes cleanly and uninstall is bounded

`TestManagerInstallRefreshStatusAndUninstall` runs `Manager.Install` twice against an isolated home and fake launchctl. It verifies the plist's canonical executable and configured arguments, the graceful `bootout` → `bootstrap` → non-forcing `kickstart` sequence on both installs, readable status, removal of the plist and canonical binary, and retention of diagnostic logs.

### Invalid installations fail before launchd registration

`TestInstallScriptRejectsMissingAppBeforeGoInstall` proves a missing configured ChatGPT bundle prevents `go install`. `TestManagerInstallRejectsNoncanonicalExecutableAndMissingApp` proves neither a disposable executable nor a missing app reaches launchctl. A separately built real CLI was also invoked from a temporary path on this Mac; it exited 1 with the canonical-path instruction, and `launchctl print gui/501/com.tylergannon.codex-autoupdate` confirmed no service was registered.

## Required checks

- `bash -n install.sh`
- installer SHA-256 matches the digest in `llms.txt`
- `go test -race ./...`
- `go vet ./...`
- `golangci-lint run ./...`
- `git diff --check`

All passed on the implementation worktree.

Claude Fable independently reviewed the implementation and proof in `ephemeral/reviews/202607171135-installer-implementation-round-04.md`, reran the gates and focused tests, and concluded that only nitpicks remain. Real launchctl behavior after bootstrap is intentionally covered by the post-tag dogfood proof below.

## Post-tag live proof

After `v0.1.1` is tagged on the squash-merged `main` commit, run both public installer paths on this Mac. Capture the public script checksum, installed build metadata/version, canonical on-disk path, plist arguments, `launchctl` status, and read-only `check --json` result. Leave the successfully proved LaunchAgent enabled. This section is intentionally pending until the public tag exists; the PR's release-proof comment will contain the resulting evidence.
