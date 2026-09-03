# App-server control socket lifecycle proof

Observed on 2026-09-03 at 08:43 CST before remediation.

## Claim: v0.2.2 can leave an old Codex runtime alive across later desktop updates

- The installed watcher reported `codex-autoupdate version v0.2.2` and had run continuously since 2026-08-24.
- The installed ChatGPT/Codex bundle was version `26.901.20858`, build `7658`.
- Updater logs recorded a successful ChatGPT/Codex update from build `7579` to build `7658` on 2026-09-02.
- PID `94932`, started on 2026-08-26, still held `$CODEX_HOME/app-server-control/app-server-control.sock`.
- `lsof -d txt` mapped PID `94932` to the deleted updater rollback bundle for build `7119`:

  `/Applications/.ChatGPT.app.codex-autoupdate-backup-7119-1787794408732383000/Contents/Resources/codex`

This establishes a mixed-generation runtime: the current desktop bundle was build 7658 while the canonical app-server control socket remained owned by executable code from build 7119.

## Claim: the candidate terminates a detached socket holder before activation

`TestQuiesceControlSocketKillsRealDetachedHolder` launched a real detached Unix-socket holder from a non-bundle path, configured it to ignore SIGTERM, and exercised the installer quiescence path. The candidate discovered it with `lsof`, escalated from SIGTERM to SIGKILL, observed zero remaining holders, and removed the socket path. The test passed on macOS.

The focused fake-runner tests also passed for normal SIGTERM exit, SIGKILL escalation, and refusing activation when a holder cannot be stopped.

## Final candidate checks before PR

- `go mod verify`
- `go vet ./...`
- `go build ./...`
- `golangci-lint run ./...` with default configuration: 0 issues
- `go test -race -count=1 ./...`
- shell syntax checks for every tracked `*.sh`
- `ephemeral/proof/check-established-bugs.sh fixed`: 4/4
- `ephemeral/proof/check-closeout-gaps.sh fixed`: 2/2
- `git diff --check main...HEAD`

All checks passed on the candidate based on v0.2.2. A release/install update and post-remediation ownership observation remain required.
