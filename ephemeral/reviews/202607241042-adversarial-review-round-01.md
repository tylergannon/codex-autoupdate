# Adversarial review: multi-harness desktop autoupdate

## Review target

- Commit `adebe38` (`feat: support multi-harness desktop updates`)
- Baseline `origin/main` at `1623f2b`
- Authoritative target: the user's Multi-Harness Desktop Autoupdate plan, including the documented `update --force` commands, the rule that no active Codex/Claude Code/Cowork work may be interrupted, safe rollback, and live repeated forced-reinstallation plus controlled rollback proof for both applications.

The user's preference to prioritize actual behavior and proof over code quality did not limit review scope; this review likewise keeps only functionality, safety, and demonstration findings.

## Evidence inspected

- Repository instructions and the full `origin/main...adebe38` diff.
- The checked-in plan, worklog, proof summary, README, and operator instructions.
- Release discovery, version comparison, CLI/configuration, LaunchAgent, coordinator, activity detection, bundle inspection, installation, shutdown, readiness, rollback, quarantine, and their tests.
- Fresh `origin/main` fetch; `adebe38` remains based on the current remote head.
- Fresh uncached `go test -count=1 ./...`, `go test -count=1 -race ./...`, and `go vet ./...`: all passed.
- Live production `check --json`, Anthropic feed response, installed bundle versions, strict code-signature identity, signing team, and Gatekeeper assessment for ChatGPT and Claude.
- Live LaunchAgent state and lock ownership, plus a non-destructive forced-command reproduction using deliberately missing application paths.
- Live process/session state relevant to Claude activity classification.
- The proof directory contents: it contains only `summary.md` and no command transcript, structured result, before/after bundle evidence, or rollback artifact.

## Findings

### 1. Critical — the documented `update --force` command cannot run while the installed LaunchAgent is operating

`settings.run` acquires the cache lock before it constructs or runs any watcher (`internal/cli/root.go:138-162`). The long-running LaunchAgent invokes `run`, is configured with `KeepAlive`, and therefore retains that same lock for its lifetime (`internal/launchagent/launchagent.go:275-294,325-338`; `internal/runlock/lock.go:15-34`). The new operator interface nevertheless documents direct invocations using the default cache (`README.md:28-38`; `llms.txt:57-70`).

This is reproducible on the installed service without risking either application. `launchctl print` reported `com.tylergannon.codex-autoupdate` running as PID 65151, and `lsof ~/Library/Caches/codex-autoupdate/watcher.lock` showed PID 65151 holding the lock. Running the branch binary with both application paths redirected to nonexistent bundles, but leaving the default cache in place, produced:

```text
error: another codex-autoupdate watcher is already running
```

The implementation worklog confirms that the live Claude proof encountered this exact problem and changed `--cache-dir` to bypass it (`ephemeral/worklog/202607241005-multi-harness-autoupdate.md:8`). That workaround is not the specified/documented command and permits two coordinators to manipulate the same application bundles concurrently, contradicting the one-coordinator-lock safety contract. Thus the central one-shot force feature is not usable in the normal installed configuration, and the live proof exercised a materially different and less safe configuration.

### 2. Issue — Claude activity detection can report idle while Claude Code work is still executing

The contract says any currently executing Claude Code or Cowork work is active and that no replacement path may run while such work exists. The detector returns idle immediately when the Claude Desktop main process is absent (`internal/activity/claude.go:34-40`). When the app is present, it scans only two Desktop metadata trees and considers work active only if a process command contains one of four identifiers from a matching metadata record (`internal/activity/claude.go:41-93,142-161`). It never detects general Claude Code sessions under `~/.claude`, nor Claude-launched child work whose command line does not contain those Desktop record identifiers.

There is a live false negative on this machine. A Claude Code session still has PID 17911 running `/bin/zsh ... npm test`, with descendants `npm test`, `tail`, and `node --test`; its output descriptor is under `/private/tmp/claude-501/.../tasks/`, and its Claude session record is under `~/.claude/projects/...`. During that execution, the production `check --json` result reported Claude `"ActiveThreads": null`. This is not merely an untested edge: the current process classifier demonstrably misses executing Claude-launched work and would allow the idle window and replacement to proceed.

### 3. Issue — rollback can leave the failed replacement running and then falsely declare the old app ready

Normal activation has the forceful shutdown fallback, but rollback does not reuse it. On readiness failure, `rollback` sends AppleScript quit, discards its result, discards any `waitForExit` error, and immediately renames the failed bundle before restoring the backup (`internal/update/installer.go:304-320`). If the failed new process refuses or fails to quit, macOS permits its on-disk bundle to be renamed while the process continues. The subsequent old-app relaunch can then target an already registered bundle identifier, while readiness checks merely find an exact command path and inspect the now-restored on-disk bundle (`internal/update/installer.go:278-301`; `internal/macos/system.go:217-225`). That can satisfy the old-build check even though the process is still the failed new executable.

The rollback test does not cover this causal path: `TestApplyRestoresOldBundleWhenReplacementDoesNotStart` sets `neverReady`, and its fixture suppresses the application process entirely when that flag is set (`internal/update/installer_test.go:151-183,354-362`). There is no test or live proof where a launched replacement is present but fails readiness and refuses graceful shutdown. Therefore rollback is not reliable for an important class of readiness failures and can report recovery without actually returning execution to the previous application.

### 4. Issue — the proof does not meet the requested definition of done and is not independently auditable

The authoritative plan required repeated live `update --force` for both applications, actual bundle replacement and signed relaunch, and controlled readiness-failure rollback; its definition of done required live forced-reinstallation and rollback proof for both applications. The checked-in plan silently weakens this to “live-safe proof” with destructive replacement requiring separate consent (`ephemeral/projects/codex-autoupdate/multi-harness-plan.md:21-28`), even though the user explicitly requested implementation of the stronger plan.

The proof itself states that ChatGPT was never replaced and that rollback was covered only by tests (`ephemeral/proof/202607241005-multi-harness/summary.md:25-34`). It also consists solely of a prose summary: there are no preserved command lines with the actual cache configuration, logs, timestamps, before/after bundle identities, process transitions, quarantine marker, or rollback transcript. Fresh inspection confirms the currently installed Claude bundle is valid, signed by the pinned team, Gatekeeper accepted, and at the advertised release, but that final state cannot establish that this updater performed the two claimed replacements or that rollback works. Combined with finding 1, even the claimed Claude force runs were performed through an undocumented lock-bypassing configuration. The core happy path is plausible and partially exercised, but the requested end-to-end demonstration is incomplete and cannot be independently checked from the committed proof.

## Outcome

material findings remain
