# Adversarial Review: system instability audit

Outcome: material findings remain

## Review Target

Current `codex-autoupdate` implementation in `/Users/tyler/src/.worktrees/codex-autoupdate/safety-audit-20260824`, reviewed against the concern that this single-machine updater may accumulate macOS instability and eventually leave Codex and Claude unable to use computer-use tooling until reboot.

I read `AGENTS.md`, `/Users/tyler/.agents/skills/agent-protocol/SKILL.md`, and `/Users/tyler/.agents/skills/adversarial-review/SKILL.md`. I ignored other untracked review artifacts in this worktree and did not rely on other reviewers.

## Evidence Inspected

- Source: `internal/update/installer.go`, `internal/macos/system.go`, `internal/watch/watch.go`, `internal/launchagent/launchagent.go`, `internal/activity/detector.go`, `internal/activity/claude.go`, tests around installer/watch behavior, `README.md`, and `llms.txt`.
- Current LaunchAgent state: `launchctl print gui/501/com.tylergannon.codex-autoupdate` showed one running agent, PID 10413, one launchd run, `KeepAlive`, `RunAtLoad`, `ThrottleInterval=60`, and stderr/stdout paths under `~/Library/Logs/codex-autoupdate/`.
- Current logs/cache: stderr log was 35,470 lines and 5,119,873 bytes; cache was 4 KB; logs directory was 5.6 MB.
- Current hidden app residue: `/Applications/.Claude.app.codex-autoupdate-1_34493_1.new` existed, was 801 MB, and had `CFBundleIdentifier=com.anthropic.claudefordesktop`, `CFBundleVersion=1.34493.1`, matching `/Applications/Claude.app`.
- Current computer-use process evidence: PID 89652 was `/Users/tyler/.codex/computer-use/Codex Computer Use.app/Contents/MacOS/SkyComputerUseService`, with open file descriptors to `/Applications/ChatGPT.app/Contents/Resources/app.asar` and multiple ChatGPT framework resources.

## Findings

### 1. Issue: ChatGPT "bundle quiescence" ignores the live computer-use service even when it holds ChatGPT bundle files open

`ProcessFinder.BundleProcesses` only returns processes whose command string starts with the app bundle path (`internal/macos/system.go:236-244`). `Installer.shutdown` signals only that set (`internal/update/installer.go:300-360`), `requireBundleQuiescent` rechecks only that same set (`internal/update/installer.go:375-383`), and `Apply` renames the app bundle immediately after that predicate passes (`internal/update/installer.go:186-198`).

Current host evidence proves that this predicate misses a real computer-use process class:

```text
PID 89652 command:
/Users/tyler/.codex/computer-use/Codex Computer Use.app/Contents/MacOS/SkyComputerUseService

lsof for PID 89652:
/Applications/ChatGPT.app/Contents/Resources/app.asar
/Applications/ChatGPT.app/Contents/Frameworks/Codex Framework.framework/.../Resources/icudtl.dat
/Applications/ChatGPT.app/Contents/Frameworks/Codex Framework.framework/.../Resources/resources.pak
```

That process does not start with `/Applications/ChatGPT.app/`, so the updater can declare the ChatGPT bundle quiescent while a computer-use service still has resources from that bundle open. The subsequent `os.Rename(i.AppPath, backupPath)` and `os.Rename(prepared.StagedPath, i.AppPath)` can then replace the application underneath that service. If the service survives the app quit, the updater has no fallback that detects or restarts it, because the fallback is command-path based.

Impact: this is a direct mechanism for leaving computer-use attached to old ChatGPT bundle resources across a desktop replacement. That can produce stale or inconsistent computer-use state until the service is restarted; a reboot is the operator-level recovery if the stale service is not otherwise managed. The quiescence gate needs to account for open-file holders or known external companion services, not only processes whose executable path is inside the bundle.

### 2. Issue: full staged app bundles are abandoned after independent updates and can accumulate in `/Applications`

There is an actual abandoned staged bundle on this host:

```text
/Applications/.Claude.app.codex-autoupdate-1_34493_1.new  801M
CFBundleIdentifier: com.anthropic.claudefordesktop
CFBundleVersion: 1.34493.1
/Applications/Claude.app CFBundleVersion: 1.34493.1
```

The log shows how it was created and then orphaned:

```text
2026-08-20T21:36:39 application replacement available harness=claude installed_build=1.34493.0 available_build=1.34493.1
2026-08-20T21:36:53 update staged and verified path=/Applications/.Claude.app.codex-autoupdate-1_34493_1.new
2026-08-20T21:36:53 waiting for active work to finish harness=claude work=[claude-task-pid:62659]
...
2026-08-20T22:31:23 application is current harness=claude installed_build=1.34493.1 available_build=1.34493.1
```

The source explains why it stayed behind. `cleanupResidue` knows how to remove `.*.codex-autoupdate-*.new` bundles (`internal/update/installer.go:574-590`), but it is called from `Prepare` (`internal/update/installer.go:91-122`). When the watcher later sees that the installed app is already current, it only calls `w.clearPrepared()` and returns (`internal/watch/watch.go:97-100`). The independent-update path also clears only in-memory state (`internal/watch/watch.go:148-151`). Neither path removes the staged bundle.

Reproduction/proof: stage an update while activity is active; update the application independently before the updater applies its staged copy; on the next watcher step, `comparison == 0 && !force` returns before any cleanup, leaving the full hidden app bundle in `/Applications`. This has already happened once on this machine.

Impact: each orphaned staged bundle is a full desktop app copy. The observed Claude orphan is 801 MB. Repeated independent updates or interrupted pending windows can accumulate multiple gigabytes under `/Applications`, causing disk pressure. Disk pressure is a concrete macOS instability vector and is aligned with the user's accumulating-instability concern.

### 3. Issue: a pending update switches the whole coordinator into 5-second full update cycles, producing avoidable feed traffic, scans, and unbounded log growth

`Coordinator.Run` calls every watcher on each loop (`internal/watch/watch.go:271-299`). If any watcher returns `Pending`, the next delay becomes `ActivityPollInterval` (`internal/watch/watch.go:302-305`). `Watcher.Step` always reinspects the installed app and fetches the latest feed before it checks whether the existing staged update is still waiting on activity (`internal/watch/watch.go:80-118`).

Runtime evidence shows the effect. After Claude staged `1.34493.1` and waited on an active Claude task, the coordinator began repeating a full loop every 5 seconds, including ChatGPT current checks:

```text
2026-08-20T21:36:53 waiting for active work to finish harness=claude work=[claude-task-pid:62659]
2026-08-20T21:36:58 application is current harness=chatgpt installed_build=6892 available_build=6892
2026-08-20T21:36:59 waiting for active work to finish harness=claude work=[claude-task-pid:62659]
2026-08-20T21:37:04 application is current harness=chatgpt installed_build=6892 available_build=6892
2026-08-20T21:37:04 waiting for active work to finish harness=claude work=[claude-task-pid:62659]
```

The LaunchAgent sends stderr to a fixed file (`internal/launchagent/launchagent.go:341-344`) and there is no rotation or truncation in the repository. Current operational state already has 35,470 stderr lines and 5,119,873 bytes. This is not just logging: in pending state the daemon repeats feed requests and activity scans at activity-poll cadence for all harnesses, not only the pending harness and not only activity detection.

Impact: a long-running task during an available update can turn a 15-minute background watcher into a 5-second feed/log/disk scanner for hours or days. That is unnecessary machine pressure for a hobby updater, and it compounds the same accumulation class as the orphaned staged bundles. The pending loop should avoid refetching stable release metadata and avoid logging unchanged "current" state for unrelated harnesses every activity tick.

## Non-Findings

- I did not find evidence of a launchd restart storm in the installed service. `launchctl` reported one run, one active PID, and no prior exit.
- I did not find accumulated update archives or quarantine markers in `~/Library/Caches/codex-autoupdate`; the current cache was 4 KB.
- I did not prove that the current 5.6 MB log alone is enough to cause computer-use failure. The material log finding is the unbounded 5-second pending-loop behavior, not the current byte count by itself.
