# Safety audit: system-instability concern

## Review target

The `codex-autoupdate` implementation at commit `6031e9f4b22039cc6a8497542c3399d03e05335a` (`v0.2.1`), its current macOS LaunchAgent, and read-only operational evidence on the host. The authoritative concern was that repeated updater activity might accumulate macOS instability and eventually leave Codex and Claude unable to use computer-use tooling until reboot.

Review constraint: this audit did not modify implementation files, application bundles, LaunchAgent state, processes, or system state. Go tests and read-only inspection commands were run only against the isolated worktree/host.

## Evidence inspected

- `internal/update/installer.go:300-383`: bundle-wide graceful quit, bounded `SIGTERM`/`SIGKILL` fallback, post-signal rechecks, and refusal to rename while bundle processes remain.
- `internal/update/installer.go:386-407` and `:440-478`: readiness polling and rollback quiescence checks.
- `internal/watch/watch.go:122-181`: activity detection, uninterrupted idle window, final activity/version preflight, and update application.
- `internal/activity/detector.go:55-127` and `internal/activity/claude.go:30-128`: Codex/Claude activity reports used by the watcher.
- `internal/launchagent/launchagent.go:325-346`: one user LaunchAgent with `KeepAlive` and `ThrottleInterval=60`.
- `go test ./...`: PASS.
- `go test -race ./...`: PASS.
- Live LaunchAgent state from `launchctl print gui/501/com.tylergannon.codex-autoupdate`: exactly one active service process (PID 10413), `runs = 1`, `last exit code = (never exited)`, with no restart loop indicated.
- Host uptime/process evidence: booted 2026-08-20 08:24:34; at audit time the updater had been running since 2026-08-20 08:33:19. There were no updater-owned zombies; the two observed zombies were children of unrelated `sshd-session` processes.
- `/Users/tyler/Library/Logs/codex-autoupdate/stderr.log`: 35,470 lines / 5,119,873 bytes; it records repeated successful update completions, including ChatGPT 6971→7019 at 2026-08-23 23:52:03 and earlier ChatGPT/Claude replacements from 2026-07-24 through 2026-08-23. The latest replacement used the bounded `SIGTERM` fallback and then completed successfully.
- Read-only installed-binary `check --json`: ChatGPT build 7019 and Claude build 1.34493.1 equal their available builds; both reports returned without errors. The current report had active Codex threads, so the watcher was not eligible to replace ChatGPT during this audit.
- Current process/file evidence: ChatGPT PID 89448 and app-server PID 89528 were running; ChatGPT had live `/tmp/codex-browser-use/*.sock` endpoints and a running `cua_node/bin/node_repl` PID 26927. Claude PID 2563 had `.../node_modules/@ant/claude-swift/.../computer_use.node` loaded. Both application bundles passed `codesign --verify --deep --strict` and `spctl --assess --type execute` (`accepted`, `Notarized Developer ID`).

## Findings

No admissible material findings remain.

The implementation does contain a deliberate forceful shutdown path: after the graceful timeout it sends `SIGTERM` to every process whose command path is under the exact application bundle, then uses `SIGKILL` only for survivors (`internal/update/installer.go:337-360`). Historical logs show that fallback has been exercised, including a 27-process ChatGPT bundle at 2026-08-20 09:04:24 and a 12-process bundle at 2026-08-23 23:52:00. This is an operationally consequential behavior, but the reviewed code rechecks the process set, waits for quiescence, and the supplied host evidence shows the subsequent replacement completed and computer-use surfaces remained live. There is no reproduced failure, accumulating process leak, updater restart loop, failed signature/readiness state, or observed loss of Codex/Claude computer-use capability attributable to it.

The current host evidence also does not show the alleged reboot-only condition: after the latest ChatGPT replacement, the GUI, app-server, browser-use sockets, and CUA node are all present; Claude's computer-use native module is loaded; and the machine has remained up for more than four days while the LaunchAgent has remained a single running instance.

## Outcome

no findings
