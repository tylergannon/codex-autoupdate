# Adversarial review: multi-harness desktop autoupdate, round 2

## Review target

- Commit `0ae4262` (`fix: complete live multi-harness proof`)
- Baseline `origin/main` at `1623f2b`
- Authoritative target: `ephemeral/projects/codex-autoupdate/multi-harness-plan.md`, including safe one-coordinator operation, an uninterrupted idle window per harness, atomic replacement with rollback, forced equal-version reinstall, and auditable live proof for both applications.

## Evidence inspected

- `AGENTS.md`, the agent protocol, the multi-harness plan, and the complete `origin/main...HEAD` file set and implementation diff.
- CLI/configuration, LaunchAgent persistence, run-lock takeover, coordinator behavior, ChatGPT and Claude activity detection, release discovery and version comparison, bundle inspection, shutdown, activation, readiness, rollback, quarantine, and their tests.
- The prior adversarial review, remediation narrative, worklog, and every committed raw live-force, live-rollback, signature, process, inode, quarantine, and fault-injection artifact under `ephemeral/proof/202607241005-multi-harness/`.
- The currently installed `dev-0ae4262` LaunchAgent: both harnesses are configured, PID 48250 owns the default lock, both production bundles are present, and current Claude `check --json` succeeds.
- Fresh `go test -count=1 ./...`, `go test -count=1 -race ./...`, `go vet ./...`, and `git diff --check origin/main...HEAD`: all passed.
- A safe temporary-cache reproduction of takeover signaling: an unrelated foreground lock owner was sent `SIGUSR1` and exited with status 158 while the one-shot command acquired its lock and returned 0.

## Findings

### 1. Critical — a second one-shot command can terminate the first one-shot during bundle replacement

Only continuous `run` processes install a `SIGUSR1` handler (`internal/cli/root.go:140-149`). Every one-shot command that encounters the lock nevertheless reads the holder PID and sends that PID `SIGUSR1` (`internal/cli/root.go:204-220`; `internal/runlock/lock.go:62-95`). The lock file records only a PID, not whether its owner is the yielding daemon or a non-yielding one-shot. Therefore, if an operator starts another `update --force` while the first is downloading, quitting, activating, or rolling back, the second command sends a default-fatal signal to the first.

This was reproduced without touching either application by placing a live PID in a temporary held watcher lock and running the built one-shot command against missing app paths. The one-shot returned 0, while the lock owner exited 158 (`128 + SIGUSR1`) and the takeover marker disappeared. The existing takeover test avoids the bug by replacing signaling with a callback that closes the lock (`internal/cli/root_test.go:103-120`).

This violates serialized safe activation. In particular, termination between the two activation renames can strand the application under its hidden backup name, and termination during rollback can leave the failed replacement active. A takeover request must distinguish a cooperative daemon from a one-shot owner, and a one-shot owner must never receive a default-fatal takeover signal.

### 2. Issue — Claude process activity does not establish the required uninterrupted idle window after work ends

The Claude detector derives `LastLifecycle` only from processes and records visible in the current poll (`internal/activity/claude.go:30-81`). When a standalone Claude Code or task-output process is present, it reports active and returns `Pending`. Once that process exits, its start time disappears from the next report; if the remaining app/session timestamp is old or absent, `LastLifecycle` is old or zero. The watcher treats a zero lifecycle as already idle and immediately proceeds to final preflight and activation (`internal/watch/watch.go:116-131,152-173`).

A two-poll sequence therefore violates the contract: poll 1 sees `claude-code-pid:N` or `claude-task-pid:N`; the process exits; poll 2, five seconds later, can replace Claude instead of starting and completing the configured five-minute idle window. The existing continuous-idle test supplies a synthetic completion timestamp (`internal/watch/watch_test.go:191-224`), but the process-only Claude sources cannot produce that timestamp. The coordinator needs per-harness observed activity state (for example, reset an `idleSince` clock whenever active is observed and start it on the first subsequent inactive poll), rather than relying solely on historical timestamps returned by detectors.

### 3. Issue — interruption between activation renames is not recovered and can leave an application permanently missing

Activation moves the installed bundle to a unique hidden backup and then performs a second rename to put the staged bundle at the canonical path (`internal/update/installer.go:149-159`). There is no transaction or restart recovery for interruption between those operations. On the next LaunchAgent start, a missing canonical application is interpreted as an intentionally uninstalled harness and silently skipped (`internal/cli/root.go:467-477`); backup residue is neither discovered nor restored by the installer cleanup path.

This misses the plan's atomic-replacement/recovery contract even without finding 1, because process termination or power loss can occur between the renames. Finding 1 makes the window directly reachable through a normal second CLI invocation. Startup should reconcile a lone validated backup/staged state, or activation should use a filesystem exchange primitive and a recoverable journal.

### 4. Issue — the proof claims successful repeated force passes that its preserved evidence does not show

The remediation says additional equal-version passes “completed successfully for both applications” (`ephemeral/proof/202607241005-multi-harness/remediation.md:96-97`). The committed force artifacts contain exactly one successful equal-version completion for ChatGPT and one for Claude (`live-chatgpt-force-once.log:1-6`; `live-claude-force.log:1-5`). The second preserved invocation for each harness is the deliberately failed rollback run, not another successful reinstall. Current installed logs also contain no second equal-version success for either harness.

The live evidence does convincingly show one successful forced replacement and one controlled rollback per harness, including changed/restored inodes, changed PIDs, signature/team/Gatekeeper checks, and no bundle residue. It does not independently support the stronger repeated-success sentence. Either preserve the missing successful-run transcripts or narrow the proof claim to what the artifacts demonstrate.

## Outcome

material findings remain
