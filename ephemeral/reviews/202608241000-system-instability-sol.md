# Adversarial review: system-instability safety audit

## Review target

- Repository state: `6031e9f` (`v0.2.1`, `origin/main`), the currently installed release.
- Authoritative concern: this single-machine updater may accumulate macOS instability until both Codex and Claude lose computer-use tooling and require a reboot.
- Operating constraint: read-only inspection of implementation and machine state; this review file is the only file I wrote. I did not run an update, stop an application, signal a process, or inspect another reviewer's artifacts.

## Evidence inspected

- End-to-end implementation: `internal/activity/detector.go`, `internal/activity/claude.go`, `internal/watch/watch.go`, `internal/update/installer.go`, `internal/macos/system.go`, `internal/cli/root.go`, and `internal/launchagent/launchagent.go`, plus focused tests around activity and bundle shutdown.
- Declared behavior: `README.md:3-5,45-50` and `llms.txt:89-117`.
- Installed state: the user LaunchAgent has been continuously running since 2026-08-20 08:33 local time with PID 10413, release `v0.2.1`, both harnesses enabled, 24 MiB RSS, 17 open file descriptors, no child processes, and no zombie child attributable to the updater.
- Updater history: `~/Library/Logs/codex-autoupdate/stderr.log` (35,470 lines). It contains 31 completed application replacements across retained history, seven current-style incomplete graceful shutdowns that fell back to `SIGTERM`, and no `SIGKILL` escalation or rollback.
- Live process and detector state: `ps`, `lsof`, and the documented read-only `codex-autoupdate check --json` command.
- Latest ChatGPT replacement lifecycle, 2026-08-23 23:51-23:52 local time: updater logs plus unified logs from `runningboardd`, `WindowServer`, `replayd`, `tccd`, and `SkyComputerUseService`.

## Findings

### [issue] Remote Claude Code tasks are reproducibly classified as idle, including a computer-use-capable task

The product promises that currently executing Claude Code work blocks replacement (`README.md:45-50`; `llms.txt:95-100`). The live detector violates that contract for the Claude remote runtime used on this machine.

Proof and reproduction:

1. At the observation, PID 28558 was a newly started Claude Code process:

   ```text
   /Users/tyler/.claude/remote/ccd-cli/2.1.237 ...
     --resume=80d8f9bf-f2a4-4266-bafa-385097d794d3
     --allowedTools mcp__computer-use,...
   ```

   It started at 2026-08-24 10:38:43 local time. Across a two-second read-only observation its accumulated CPU time increased from `0:06.02` to `0:06.05`, so this was an executing runtime, not merely a command string copied from a file.

2. While that process was present and executing, the installed `v0.2.1` command documented as read-only returned the following Claude activity state:

   ```json
   {
     "AppServerPID": 2563,
     "ActiveThreads": null,
     "LastLifecycle": "2026-08-21T04:35:38.538-06:00"
   }
   ```

   Reproduction command:

   ```sh
   "$HOME/Library/Application Support/codex-autoupdate/codex-autoupdate" check --json
   ```

3. The result follows directly from the classifier. `internal/activity/claude.go:131-154` recognizes only an executable named `claude` or `claude-code`, an executable path containing `/.local/share/claude/versions/`, or a later argument containing `/@anthropic-ai/claude-code/`. The observed executable's basename is `2.1.237` and its path is under `~/.claude/remote/ccd-cli/`, so none match. The other detector routes (`internal/activity/claude.go:65-125`) also failed to associate this process, as the live `ActiveThreads: null` result demonstrates.

4. `internal/watch/watch.go:122-138` blocks only when this report is active or its lifecycle is inside the idle window, and the final preflight repeats the same predicate at lines 159-180. With the reported lifecycle more than three days old, an available Claude release (or the supported force path) deterministically advances to `Installer.Apply` while this task is executing.

Impact: an executing remote Claude Code task, even one explicitly provisioned with `mcp__computer-use`, receives no idle protection. The updater may replace and restart Claude Desktop underneath it. This is a concrete safety-contract failure and a credible interruption path for computer-use work. It does **not**, by itself, prove cumulative macOS corruption or that a reboot would be required.

## Disposition of the broader instability concern

I found no admissible evidence that current `v0.2.1` accumulates stale ChatGPT bundle processes, stale computer-use services, or stale ScreenCaptureKit clients:

- The latest update requested graceful shutdown at 23:51:30, timed out, sent `SIGTERM` to all 12 processes executing under `/Applications/ChatGPT.app` at 23:52:00.896, and completed replacement at 23:52:03.192. No `SIGKILL` occurred.
- The out-of-bundle old `SkyComputerUseService` PID 10147 exited at 23:52:00.985. `replayd` invalidated its connection at 23:52:01.001, ran `stopAllStreamsWithError`, reported that the disconnected client's active session was successfully stopped, and removed/deallocated the client by 23:52:01.139.
- The replacement service PID 89652 was tracked at 23:52:07.869. `replayd` accepted it and completed a ScreenCaptureKit shareable-content fetch by 23:52:07.919. Current process inspection shows exactly one `SkyComputerUseService`, no process executing from a backup/failed/staged application path, and no retained old computer-use service.

The implementation also requires zero processes executing from the old app bundle before either activation or rollback (`internal/update/installer.go:300-383,439-477`), and live evidence shows that cleanup worked for the most recent forced shutdown. Therefore I do not claim the updater caused the reported reboot-only, cross-application computer-use failure. The one material finding above is a proven idle-gate hole; any stronger causal claim about accumulating macOS instability would be speculation on the available evidence.

## Outcome

material findings remain
