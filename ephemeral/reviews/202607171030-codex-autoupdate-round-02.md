# Adversarial review — codex-autoupdate, round 02

- Date: 2026-07-17 10:30 local
- Reviewer: adversarial-review skill (read-only; only this artifact written)
- Target: full working tree at `codex/implement-autoupdate`, HEAD `87735bd`
  ("fix: harden failed update recovery"), clean tree
- Scope: re-verify every round-01 finding
  (`ephemeral/reviews/202607171010-codex-autoupdate-round-01.md`), sweep the
  full tree for regressions and new material issues

## Evidence inspected

- Full diff of `87735bd` and the current contents of every changed file:
  `internal/update/installer.go`, `internal/activity/detector.go`,
  `internal/appcast/appcast.go`, `internal/watch/watch.go`,
  `internal/cli/root.go`, all touched tests, `README.md`,
  `ephemeral/projects/codex-autoupdate/human-proof.md`, worklog
- Unchanged files re-checked for interaction regressions:
  `internal/launchagent/launchagent.go`, `internal/macos/system.go`,
  `internal/runlock/lock.go`, `cmd/codex-autoupdate/main.go`,
  `ephemeral/projects/codex-autoupdate/live-proof.sh`
- Verification performed (read-only; probes via `go test -overlay`, no project
  files touched, tree confirmed clean after):
  - `go build ./...`, `go vet ./...`, `go test ./...` — all pass
  - Round-01 F3 repro re-run: a blank line mid-rollout now yields idle report,
    no error (fixed)
  - New probe: corrupt terminal record → thread stays active (fail-safe) and a
    warning is surfaced in `Report.Warnings` (matches worklog decision)
  - New probe: corrupt `session_meta` first line → file silently skipped
    (see N1)
  - Proof-mechanism grep strings re-checked against current log messages
    (`watch.go:137`, `installer.go:142,170`) — `live-proof.sh` still matches

## Round-01 findings — status

- **F1 (critical: endless failed-update retry loop + unbounded /Applications
  residue) — FIXED.** `Prepare` now consults a persisted per-build quarantine
  marker (`installer.go` `failureReason`/`failurePath`,
  `~/Library/Caches/codex-autoupdate/failed-build-<build>.json`) and refuses a
  quarantined build with a clear recovery message; `rollback` and
  `abortActivation` write the marker (with an in-memory fallback if the write
  fails) and delete the failed/staged bundle immediately; `cleanupResidue`
  sweeps stale `.new` and `failed-*` bundles on every `Prepare`. A newer build
  remains eligible; deliberate marker removal re-enables retry
  (covered by `TestApplyRestoresOldBundleWhenReplacementDoesNotStart` and the
  residue assertions in `TestPrepareDownloadsExtractsVerifiesAndStages`).
  README and human-proof updated consistently.
- **F2 (minimumSystemVersion unenforced) — FIXED.** `appcast.Latest` now
  resolves the host version (`sw_vers`, overridable via `HostVersion`),
  validates numeric forms, and skips items whose `minimumSystemVersion`
  exceeds the host (`appcast.go:89-94`); non-numeric requirements are skipped
  conservatively. Tests cover selection, comparison, and validation.
- **F3 (blank/corrupt rollout line permanently disables detection) — FIXED.**
  Blank lines are skipped; undecodable complete records are counted and
  surfaced as warnings instead of erroring (`detector.go:167-206`); a corrupt
  terminal record leaves the thread active (fails safe, blocks the update)
  with a logged warning (`watch.go:151-155`). Reproduced both behaviors by
  probe.
- **F4 (post-quit failures strand the user with no app) — FIXED.** The
  installed bundle is now deep-verified *before* the quit is requested
  (`installer.go:127-130`); quit-request failure and both activation-rename
  failure paths relaunch the previous app (`relaunchPrevious`,
  `abortActivation`), covered by
  `TestApplyRelaunchesPreviousAppWhenActivationRenameFails`.
- **F5 (`err == context.Canceled`) — FIXED.** `errors.Is` at
  `cli/root.go:103`.

## Findings (new material issues: none; genuine nitpicks only)

### N1 — nitpick: a corrupt `session_meta` line silently hides a rollout's activity and suppresses its warning

Evidence (probe-confirmed): if the first line of a Desktop rollout is
undecodable, `state.originator` stays empty, so the originator filter at
`detector.go:107` drops the file *before* the warning append at
`detector.go:110-112` — an active `task_started` in that file is invisible and
no warning is logged (fail-open, unlike the fail-safe corrupt-terminal case).
Mitigating: `session_meta` is written once at file creation, far from the
append frontier where torn writes occur, and treating unknown-originator files
as Desktop would wrongly let CLI sessions block updates. Consider emitting the
warning before the originator filter for files with corrupt records.

### N2 — nitpick: host-side transient activation failures quarantine a good build

`abortActivation` (`installer.go:311-323`) writes a quarantine marker even when
the cause is environmental (e.g. a transient rename/permission failure moving
the *installed* app), not a defect of the release. The build then stays blocked
until the marker is removed or a newer release ships. Bounded, clearly
messaged, and recoverable — but it can delay a perfectly good update.

### N3 — nitpick: quarantine markers live in `~/Library/Caches`, which macOS may purge

`failurePath` places markers in `CacheDir` (default
`~/Library/Caches/codex-autoupdate`). A storage-pressure cache purge deletes
them, silently re-enabling one retry of a quarantined build; the in-memory
fallback is also lost on process restart when the marker write failed.
Application Support would be a more durable home for state that suppresses
disruptive behavior.

### N4 — nitpick: orphaned `-backup-` bundles are never swept

`cleanupResidue` (`installer.go:326-347`) globs only `*.new` and `failed-*`
patterns. A backup bundle left behind by the warn-only path at
`installer.go:165-167` ("rollback bundle could not be removed") or the
catastrophic double-rename path persists in `/Applications` forever. One-off
and warned about, but permanent.

### N5 — nitpick: a single >16 MiB rollout line still hard-fails detection

`detector.go:164-166` returns an error (propagated out of `Detect`) for an
oversized line instead of counting it as a corrupt record like every other
malformed input. Practically implausible, but it is the one remaining input
that reproduces round-01 F3's fail-hard mode.

## Outcome

no material findings
