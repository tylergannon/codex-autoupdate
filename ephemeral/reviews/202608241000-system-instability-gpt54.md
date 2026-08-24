# Adversarial Review

- Reviewer: adversarial-review skill, read-only except for this artifact.
- Review target: the current `codex-autoupdate` implementation at `6031e9f4b22039cc6a8497542c3399d03e05335a` (`v0.2.1`) plus live read-only host evidence, against the concern that this updater may accumulate macOS instability until Codex and Claude lose computer-use capability and require a reboot.
- Outcome: material findings remain.

## Evidence inspected

- Repository instructions: `AGENTS.md`, `/Users/tyler/.agents/skills/agent-protocol/SKILL.md`.
- Product contract: `README.md:45-50`, `llms.txt:95-123`.
- Claude activity path: `internal/activity/claude.go`, `internal/macos/system.go`, `internal/watch/watch.go`, `internal/cli/root.go`, and `internal/activity/claude_test.go`.
- Update and shutdown path: `internal/update/installer.go`, `internal/update/installer_test.go`.
- Live LaunchAgent and installed runtime:
  - `launchctl print gui/501/com.tylergannon.codex-autoupdate`
  - `~/Library/Application Support/codex-autoupdate/codex-autoupdate --version`
  - `~/Library/Application Support/codex-autoupdate/codex-autoupdate check --json`
- Live process state:
  - `ps -axo pid=,ppid=,lstart=,rss=,state=,command= ...`
  - `lsof -n -p 10413`
  - `lsof -n -a -p28558 -d0,1,2`
  - `lsof -n -a -p89652`
  - `find /Applications -maxdepth 1 ...codex-autoupdate...`
- Operational logs:
  - `~/Library/Logs/codex-autoupdate/stderr.log`
  - `/usr/bin/log show --style compact --start '2026-08-23 23:51:45' --end '2026-08-23 23:52:15' --predicate 'process == "replayd" || eventMessage CONTAINS[c] "SkyComputerUse"'`

Note: an initial broad `rg` over the worktree surfaced filenames/snippets from existing `ephemeral/reviews/*` artifacts before I narrowed scope. I did not use those artifacts as evidence below; the findings here are reconstructed from code, docs, and live read-only machine state.

## Findings

### 1. [issue] Executing remote Claude Code tasks are classified idle, so the updater can restart Claude Desktop underneath live computer-use work

The product contract says currently executing Claude work blocks replacement (`README.md:45-50`, `llms.txt:95-100`). The live detector on this machine violates that contract for the remote Claude runtime that is actually in use.

Evidence and proof:

1. During the audit, the host had executing remote Claude Code processes:
   - `ps -axo pid=,ppid=,command= | rg '/Users/tyler/\.claude/remote/ccd-cli/'` showed PID `28558` and PID `80886` running `/Users/tyler/.claude/remote/ccd-cli/2.1.237 ...`.
   - Those commands included `--allowedTools mcp__computer-use,...`, so this was not a dormant GUI-only Claude session.
2. The installed updater's read-only report simultaneously said Claude was idle:
   - `codex-autoupdate check --json` returned Claude activity
     - `ActiveThreads: null`
     - `LastLifecycle: "2026-08-21T04:35:38.538-06:00"`
   - That is three days stale even though the remote Claude task above was started on `2026-08-24 10:38:43`.
3. The process-only detector cannot recognize this runtime. `internal/activity/claude.go:57-63` treats a process as active only if `isClaudeCodeProcess` returns true, and `internal/activity/claude.go:131-154` accepts only:
   - basename `claude` or `claude-code`
   - an executable path containing `/.local/share/claude/versions/`
   - a later argv field containing `/@anthropic-ai/claude-code/`
   The observed executable path `/Users/tyler/.claude/remote/ccd-cli/2.1.237` matches none of those conditions.
4. The stdio fallback also misses the live remote process. `internal/activity/claude.go:69-80` asks `OpenFilesUnder` for PIDs whose fd `0/1/2` are open under `/private/tmp/claude-$uid`, and `internal/macos/system.go:247-273` implements that literally. But `lsof -n -a -p28558 -d0,1,2` showed fd `0`, `1`, and `2` are all anonymous `PIPE`s, not files under `/private/tmp/claude-501/`.
5. Once Claude is misreported idle, the watcher has no further protection. `internal/watch/watch.go:122-181` blocks only on `report.Active()` or a restarted idle window; otherwise a newer release or forced equal-version pass proceeds into `Installer.Apply`.

Impact:

- A live remote Claude Code task, including one explicitly granted `mcp__computer-use`, is not protected from updater shutdown/restart.
- That is a reproduced safety-contract failure and a credible way to interrupt live computer-use work.
- I did not prove the stronger claim that this bug accumulates machine-wide instability or forces a reboot; the admissible impact is incorrect idle gating and task interruption.

## No additional admissible instability finding

I did not find admissible evidence that the current `v0.2.1` shutdown/replacement path is itself accumulating stale computer-use services, stale replayd sessions, or updater process leakage toward a reboot-only failure:

- `launchctl print gui/501/com.tylergannon.codex-autoupdate` showed exactly one running LaunchAgent process (`pid = 10413`, `runs = 1`, `last exit code = (never exited)`).
- `ps` and `lsof` showed that updater PID `10413` has been running since `Thu Aug 20 08:33:19 2026`, with 17 open file descriptors and no child processes.
- `~/Library/Logs/codex-autoupdate/stderr.log` shows the latest ChatGPT replacement on `2026-08-23` requested graceful shutdown at `23:51:30.699`, escalated to `SIGTERM` at `23:52:00.896`, and completed at `23:52:03.192`. I found no `sending SIGKILL` line in the inspected log output.
- The targeted `log show` window around that replacement shows the old `SkyComputerUseService` process `10147` exited at `23:52:00.985`; `replayd` invalidated the connection at `23:52:01.001`, ran `stopAllStreamsWithError`, reported the disconnected client's active session was successfully stopped, and removed/deallocated the client by `23:52:01.139`.
- The same log window then shows the replacement `SkyComputerUseService` PID `89652` was accepted by `replayd` at `23:52:07.905`.
- Current process inspection shows exactly one live `SkyComputerUseService` (PID `89652`), no process executing from a `codex-autoupdate-backup`, `codex-autoupdate-failed`, or old hidden ChatGPT/Claude bundle path, and both ChatGPT and Claude are running current signed bundles.

## Nitpick

### N1. Current-version staged bundles can remain in `/Applications` indefinitely

`find /Applications -maxdepth 1` found `/Applications/.Claude.app.codex-autoupdate-1_34493_1.new`, and its `Info.plist` reports build `1.34493.1`, the same build currently installed. This is consistent with code: `watch.Step` returns early when the installed app is already current (`internal/watch/watch.go:97-100`), while residue cleanup only runs from `Installer.Prepare` (`internal/update/installer.go:101-112,574-595`). So once the current staged bundle exists, steady-state polling never removes it until another prepare cycle happens. I do not consider this a system-instability bug; it is bounded disk residue.
