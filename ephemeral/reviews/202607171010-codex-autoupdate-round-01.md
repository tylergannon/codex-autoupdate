# Adversarial review — codex-autoupdate, round 01

- Date: 2026-07-17 10:10 local
- Reviewer: adversarial-review skill (read-only; only this artifact written)
- Target: full working tree at `codex/implement-autoupdate`, HEAD `1bc21ae` (clean tree)
- Goal reviewed against: a macOS user LaunchAgent that waits for Desktop Codex
  task idleness before safely updating and restarting ChatGPT Desktop
  (AGENTS.md, README.md, `ephemeral/projects/codex-autoupdate/research.md`)

## Evidence inspected

- Instructions: `AGENTS.md`, agent protocol skill, `README.md`, `lefthook.yml`
- Research: `ephemeral/projects/codex-autoupdate/research.md`, worklog
  `ephemeral/worklog/202607170952-codex-autoupdate.md`
- Implementation: `cmd/codex-autoupdate/main.go`, `internal/cli/root.go`,
  `internal/activity/detector.go`, `internal/appcast/appcast.go`,
  `internal/launchagent/launchagent.go`, `internal/macos/system.go`,
  `internal/runlock/lock.go`, `internal/update/installer.go`,
  `internal/watch/watch.go`, all `*_test.go`
- Proof mechanism: `ephemeral/projects/codex-autoupdate/human-proof.md`,
  `ephemeral/projects/codex-autoupdate/live-proof.sh`
- Verification performed: `go build ./...`, `go vet ./...`, `go test ./...` all
  pass; live-proof grep strings cross-checked against actual `slog` messages in
  `watch.go`/`installer.go` (all three match); `jq` paths in `live-proof.sh`
  cross-checked against the untagged JSON field names emitted by `check --json`
  (`.installed.Build`, `.available.Build`, `.activity.AppServerPID` all exist);
  finding 3 reproduced with an out-of-tree `go test -overlay` probe (no project
  files touched, tree verified clean afterward).

## Findings

### F1 — critical (race/antipattern): persistent update failure loops forever, repeatedly quitting the app and abandoning full-size failed bundles in /Applications

Evidence: `internal/update/installer.go:250-267` (`rollback` moves the failed
bundle to a unique `.ChatGPT.app.codex-autoupdate-failed-<build>-<unixnano>`
path that is never cleaned by any code path; `removeExact` is only ever applied
to the current build's staged path and the success-path backup),
`internal/watch/watch.go:50-63` (`Run` logs a cycle error and retries every
`PollInterval` with no failure memory or backoff).

Causal chain: `launchAndWait` (`installer.go:224-248`) defines readiness as a
process whose argv starts with `<app>/Contents/Resources/codex ` and contains
`app-server` (`internal/macos/system.go:162-180`) plus the expected
`CFBundleVersion`. `research.md:46` itself concedes the app-server spawn is
"not [a] public contract". If any future release relocates or renames the
embedded `codex` binary or changes how the app-server is spawned — or the
release is incompatible with the host (see F2) — readiness can never succeed
even when the update itself is healthy. The watcher then performs, every 15
minutes, forever: wait for idle → quit the user's app → replace → wait 90 s →
rollback → relaunch, leaving one new multi-hundred-MB hidden failed bundle in
`/Applications` per attempt. That is unbounded disk growth plus a recurring,
automated interruption of the user's work — exactly the harm this tool exists
to prevent. Related residue gap: superseded staged bundles
`.ChatGPT.app.codex-autoupdate-<oldbuild>.new` are never removed either,
because `stagedPath` is per-build and `Prepare` only clears the current build's
path (`installer.go:50, 289-291`).

Impact: unbounded `/Applications` disk consumption; repeated disruptive app
restarts with no escalation, cap, or persisted "this build failed" marker.

### F2 — issue (incomplete requirement): `minimumSystemVersion` is parsed but never enforced

Evidence: `internal/appcast/appcast.go:21,102,133` populate
`Release.MinimumSystem`; `grep` confirms no consumer anywhere in the tree.
`Latest` filters items only by architecture (`appcast.go:76-78`).
`research.md:16` records that the appcast supplies a "minimum macOS version"
as part of the update metadata; Sparkle's own updater gates on it. If OpenAI
ships a build requiring a newer macOS than the host, this watcher selects it,
stages it (signature and Gatekeeper checks pass — they do not check OS
compatibility), quits the app, installs it, and the replacement fails
readiness, feeding directly into F1's permanent quit/rollback loop.
(`SparkleSignature` is likewise parsed and dropped; that one is acceptable
because README's documented safety model relies on codesign + team ID +
Gatekeeper instead, but `MinimumSystem` has no compensating check.)

Impact: a legitimately published but host-incompatible release converts the
watcher into a recurring outage generator.

### F3 — issue (verifiable bug, reproduced): one blank or corrupt non-final line in any recent Desktop rollout permanently disables activity detection

Evidence: `internal/activity/detector.go:163-170` — any line that fails
`json.Unmarshal` is fatal unless it is the final unterminated line
(`readErr == io.EOF`); a blank line (`"\n"`) mid-file fails with "unexpected
end of JSON input". The error aborts the entire `Detect`
(`detector.go:99-101, 116-118`). Because the mtime cutoff is pinned to
app-server start (`detector.go:74`) and an actively appended file keeps a
recent mtime, the failure never ages out for the server's lifetime.

Reproduction (confirmed via an overlay test against the real package):

```
printf '\n' >> ~/.codex/sessions/<recent Desktop rollout>.jsonl
./codex-autoupdate check        # fails: decode JSONL record: unexpected end of JSON input
```

Probe output: `err=read rollout .../rollout.jsonl: decode JSONL record:
unexpected end of JSON input` for a file containing session_meta, one blank
line, and a valid task_complete record.

Impact: `check` errors out entirely; `waitForIdle` errors every cycle so
updates never proceed (error logged every 15 minutes); the tool silently stops
doing its one job until the Desktop app restarts *and* the offending file's
mtime falls behind the new cutoff. It fails safe (never updates while blind),
but a single stray byte in one of many JSONL files the tool does not own
bricks it. A per-file skip-with-warning for undecodable *lines* (while still
failing closed on files that ever showed `task_started` without a terminal
event) would be strictly safer.

### F4 — issue (safety-model gap): mid-transaction failures after the graceful quit leave the user with no running app

Evidence: `internal/update/installer.go:127-141`. After Desktop has been quit,
`Inspect(appPath, verify=true)` runs deep codesign + `spctl` (network-touching,
can fail transiently) + `lipo` on the old bundle; if that inspection fails, or
if the first `os.Rename` fails, `Apply` returns an error without relaunching
the app it just quit. README's safety model (lines 53-54) promises "no
replacement happens" on quit timeout and restore-and-relaunch on failed
replacement, but this intermediate window does neither: the user's app simply
stays closed until they notice. Secondary effect: performing the heavyweight
verification of the *old* bundle between quit and rename needlessly widens the
downtime window — it could run before requesting the quit.

Impact: violates the implicit "the app is left running unless the update
succeeds or is rolled back" contract on plausible transient failures.

### F5 — nitpick: wrapped context cancellation is reported as a failure

Evidence: `internal/cli/root.go:101-104` compares `err == context.Canceled`,
but cancellation surfacing through activity detection is wrapped
(`watch.go:119` `fmt.Errorf("inspect Desktop task activity: %w", ...)`;
`detector.go` returns `ctx.Err()` through `WalkDir`). So Ctrl-C/SIGTERM during
an activity poll exits 1 and prints `error: inspect Desktop task activity:
context canceled`, while the same signal during a sleep exits 0. Use
`errors.Is(err, context.Canceled)`.

## Notes (not findings)

- The proof mechanism is coherent: preconditions gate on a genuinely newer
  appcast build, the script's exit-zero criteria match the README claims, its
  grep targets match the real log strings, and its `jq` paths match the actual
  JSON shape. Its honesty about being deferred until a real update exists is
  appropriate; fabricating an update would not prove the OpenAI-signed path.
- The acknowledged millisecond-scale activity race is re-checked in `Apply`'s
  preflight and documented in README and research, as required.
- AGENTS.md protocol observance (worktree, worklog, ephemeral usage,
  checkpoint commits) looks satisfied; build, vet, and tests pass.

## Outcome

material findings remain
