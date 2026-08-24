# System-instability safety review

## Target and scope

Reviewed `codex-autoupdate` at `6031e9f4b22039cc6a8497542c3399d03e05335a` (`v0.2.1`) against the concern that this per-user updater may accumulate macOS instability until Codex and Claude computer-use tooling fails. The scope included the watcher, activity detectors, LaunchAgent installation, updater residue handling, current tests, and read-only evidence from the installed LaunchAgent, logs, processes, and application volume.

No implementation, configuration, LaunchAgent, process, or installed artifact was changed. I did not find an unreaped-child or duplicate-daemon leak: the installed job had one running updater PID, its command probes use synchronous `CombinedOutput`, and the updater had 17 open file descriptors at inspection.

Evidence inspected:

- `internal/cli/root.go`, `internal/watch/watch.go`, `internal/activity/detector.go`, `internal/update/installer.go`, and `internal/launchagent/launchagent.go`, with their relevant tests.
- The installed `gui/501/com.tylergannon.codex-autoupdate` job: one running PID (`10413`), `--poll-interval 15m0s`, and `--activity-poll-interval 5s`.
- `/Users/tyler/Library/Logs/codex-autoupdate/stderr.log`: 5,119,873 bytes and 35,470 lines at 2026-08-24T10:37:17-06:00. It contains 14,245 `waiting for active work to finish` and 19,795 `application is current` records; consecutive active-work records on 2026-08-20 were approximately 5.36 seconds apart.
- The live, unreferenced `/Applications/.Claude.app.codex-autoupdate-1_34493_1.new`: 820,624 KiB. Its and `/Applications/Claude.app`'s `CFBundleVersion` are both `1.34493.1`; no process or open file used the hidden bundle when inspected.

## Findings

### Critical — LaunchAgent diagnostic output grows without a limit or lifecycle

Evidence:

- The daemon builds a text `slog` handler on `command.ErrOrStderr()` in `internal/cli/root.go:157`.
- The installed plist template binds that stream permanently to `StandardErrorPath` in `internal/launchagent/launchagent.go:341-344`; live FD 2 is that `stderr.log` file.
- Every normal current-build pass logs at `internal/watch/watch.go:97-100`; pending active work logs at `:128-130`; the coordinator selects the five-second activity interval after any watcher returns `Pending` at `:289-316`.
- There is no rotate, truncate, size limit, or removal of these paths. The uninstall test explicitly asserts that logs are retained (`internal/launchagent/launchagent_test.go:110-124`).

Complete proof / reproduction:

1. Install the normal two-harness service and allow an eligible release to be staged while its work remains active. `Watcher.Step` returns `Pending` after emitting the active-work log record.
2. `Coordinator.Run` then sleeps for `ActivityPollInterval` (five seconds in the installed job) and calls `Step` again, which emits another record. The live log demonstrates exactly this sequence: e.g. Claude records at 22:24:12.101, 22:24:17.445, and 22:24:22.882 on 2026-08-20.
3. Each record is written to the same persistent FD 2. No code path decreases that file's length. Therefore after any number `n` of pending passes it has gained at least `n` non-empty records. The normal 15-minute current-build path also grows it indefinitely.
4. The volume is finite, so a sufficiently long-lived machine or a long pending task consumes all remaining space. This is not a race or a hypothetical leak; the installed machine has already accumulated 5.1 MB / 35,470 lines this way.

Impact: disk exhaustion and the resulting failure of app caches, IPC, and browser/computer-use helpers is a direct system-wide failure mode. The audit cannot attribute a past computer-use outage to this file alone, but the updater has an unbounded, presently exercised route to that outcome.

### Issue — An independently completed update leaves a full staged app bundle indefinitely

Evidence:

- `Watcher.Step` forgets staged state only in memory when the candidate equals the installed build (`internal/watch/watch.go:97-100`) and when an independent update wins (`:148-155`). `clearPrepared` only nils fields (`:189-191`).
- The only cleanup of `.codex-autoupdate-*.new` bundles is in `Installer.Prepare` (`internal/update/installer.go:101-112`, `:574-594`). A current release does not call `Prepare`.
- `TestWatcherForceAbandonsStagedWorkAfterIndependentUpdate` intentionally reaches this branch but its fake installer has no cleanup assertion (`internal/watch/watch_test.go:106-127`).
- The production `/Applications/.Claude.app.codex-autoupdate-1_34493_1.new` is a concrete instance: it is 801 MiB, matches the installed `Claude.app` build `1.34493.1`, and remains after the daemon has repeatedly logged that both installed and available builds are current.

Complete proof / reproduction:

1. Let `Prepare` stage release `N` and let activity keep the watcher pending.
2. Claude or ChatGPT updates itself to `N` before the updater applies its staged copy.
3. The next `Step` takes the equality branch, invokes only `clearPrepared`, and returns. It cannot invoke `cleanupResidue` because that is reachable only from a subsequent `Prepare` for a non-current candidate.
4. Until a later update becomes eligible, the hidden full app bundle is neither running nor removed. A daemon restart has the same result because no startup sweep exists.

Impact: this immediately removes 801 MiB of the currently available 96 GiB and can retain one such heavyweight bundle per harness for an arbitrary time. It compounds the unbounded artifacts below and reduces headroom for the desktop apps' own computer-use state.

### Issue — Failed-release archives and retained rollback bundles have no eventual garbage collection

Evidence:

- `download` atomically promotes each archive to `<cache>/<App>-<build>.zip` at `internal/update/installer.go:241-278`. The only normal archive removal is after successful extraction, verification, staging, and copy at `:158-160`.
- `cleanupResidue` scans only `.new` and `.failed-*` paths in the application parent (`:574-594`): it never inspects cache ZIPs or `.codex-autoupdate-backup-*` bundles.
- A failed replacement that cannot be stopped deliberately retains its old backup; this is reproduced by `TestRollbackDoesNotMoveBundlesWhenFailedReplacementCannotStop`, which asserts that one backup remains (`internal/update/installer_test.go:510-532`). Later successful apply removes only the backup path created for that apply (`internal/update/installer.go:193-208`).

Complete proof / reproduction:

1. Supply a length-correct archive for release `N` that does not contain the expected app bundle. `download` promotes `App-N.zip`; `findExtractedApp` then returns at `internal/update/installer.go:137-139` before the only archive removal. The archive remains.
2. On a later failed release `N+1`, the filename differs. `Prepare` calls `cleanupResidue`, but its two application-directory patterns cannot match either cache ZIP. Repeat for distinct builds: every promoted archive remains, so retained byte count is the sum of all failed archives.
3. Independently, after the test-proven rollback-refusal path leaves backup `B1`, allow that replacement to stop and apply a later release successfully. The later success deletes only newly-created `B2`; the code has no path that deletes `B1` while `AppPath` exists. Repeating the sequence retains one full old app per failed rollback.

Impact: both paths are unbounded on a machine that encounters multiple malformed, incompatible, or unrecoverable releases. Desktop-app archives and bundles are large enough that this provides another concrete disk-exhaustion route even if logs are rotated.

### Issue — The Codex activity detector monotonically retains one cache entry per rollout for the app-server lifetime

Evidence:

- `Detector` owns `cache map[string]cachedRollout` (`internal/activity/detector.go:39-52`). It is initialized when the app-server PID changes (`:66-74`).
- Every matching rollout path is inserted at `:98-105`.
- The only reset is no server or changed PID; there is no deletion for completed, archived, removed, or old-enough rollout paths. `Detect` continues to walk both session trees on every check (`:76-124`).

Complete proof / reproduction:

1. Start one desktop app-server PID, which allocates an empty cache.
2. Create a Codex Desktop rollout file after its start. The next detector pass inserts that path.
3. Complete or archive the thread. Its cached state becomes inactive, but neither branch deletes the map key.
4. Repeat with `k` distinct rollouts while the PID remains alive. The map contains at least `k` distinct keys. No bound, TTL, or eviction condition exists before an app-server restart.

Impact: a long-lived ChatGPT process and active update polling accumulate watcher memory and repeatedly traverse growing Codex session trees. This is a genuine monotonic in-process retention path; it is smaller per item than the disk defects, but it adds avoidable pressure precisely while the updater is polling for a safe desktop restart.

## Outcome

material findings remain
