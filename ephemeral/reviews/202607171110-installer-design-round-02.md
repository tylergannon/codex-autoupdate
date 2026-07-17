# Adversarial review — installer redesign design doc, round 02

- Date: 2026-07-17 11:10 local
- Reviewer: adversarial-review skill (read-only; only this artifact written)
- Target: revised `ephemeral/projects/codex-autoupdate/installer-design.md`
  (untracked, worktree `installer-redesign`, base `23d8f13`)
- Scope: re-verify every round-01 finding
  (`ephemeral/reviews/202607171100-installer-design-round-01.md`), then
  re-review the full design against the user correction, repository
  instructions, and current implementation — not only prior findings

## Evidence inspected

- Full revised design doc (all 53 lines) and the updated worklog
  (`ephemeral/worklog/202607171055-installer-redesign.md`, review line)
- Implementation the design changes: `internal/cli/root.go`,
  `internal/launchagent/launchagent.go` (confirmed `requireAdminUser` and
  `launchctl` calls are currently hardcoded — the promised injection seams are
  new work), `internal/launchagent/launchagent_test.go` (today only
  `renderPlist` is tested; no Manager-level seams exist yet),
  `cmd/codex-autoupdate/main.go`, `internal/watch/watch.go`,
  `internal/update/installer.go`
- Docs being replaced: `README.md`, `llms.txt`; proof material
  `human-proof.md`, `live-proof.sh`
- Repo state: `install.sh` does not exist yet (design-only review); only tag
  is `v0.1.0`

## Round-01 findings — status

- **F1 (install-dir override contradicts canonical-path refusal) — FIXED.**
  The public override is removed (line 29: "There is no install-directory
  override: the script, CLI, plist, and uninstaller share one canonical
  location"); isolation for Go tests is respecified as internal dependency
  injection (line 41: isolated home, UID, admin check, fake launchctl runner
  as "Go dependencies, not public installation overrides"). Script and CLI
  now converge on directory-services home resolution (line 25 vs.
  `user.Current()` in cgo builds), closing the `$HOME`-divergence corner.
  A residue of this fix is examined as new finding F1 below.
- **F2 (missing ChatGPT Desktop preflight) — FIXED.** Line 24 requires
  admin and `/Applications/ChatGPT.app` before installing anything; line 35
  has the CLI independently re-verify admin, the configured app path, and
  the canonical executable; proof claim 5 proves the negative path fails
  "before registering a LaunchAgent".
- **F3 (flag forwarding unproved; audited command could not carry flags) —
  FIXED.** Lines 13–18 publish the `bash -s --` form as part of the
  user-facing contract; proof claim 3 requires forwarded non-default flags to
  appear in the installed plist; line 52 adds plist-with-forwarded-
  configuration to the pre-release Manager tests.
- **F4 (curl|bash hygiene) — PARTIALLY FIXED.** Line 31 adds the
  `main`-guard against truncated execution and tells interactive agents to
  fetch and inspect first. The mutable-tag pin itself remains (see N1).
- **F5 (`kickstart -k` could kill a watcher mid-update) — FIXED.** Line 35:
  graceful replacement of an existing LaunchAgent, bootstrap, and kickstart
  without `-k`. Combined with the watcher's SIGTERM handling (`main.go:17`,
  ctx-aware commands in `Apply`), the rename window concern is adequately
  closed at design level.

## Findings

### F1 — issue (incomplete design): the shell-bootstrap test plan has no isolation mechanism, and the fixes that removed the overrides are exactly what make a naive test run destructive

Line 52 promises: "The shell bootstrap is tested with a fake `go` command and
installed binary after its preflight functions are separately exercised." But
the design now (deliberately) closes every door such a test could use to
avoid the real installation: there is no install-directory override (line
29), and the script resolves the home directory through macOS directory
services (line 25), which ignores a sandboxed `$HOME`. So a full-script test
run on the development Mac — fake `go` writing a stub to `$GOBIN`, stub
binary answering `install` — writes that stub to the *real*
`~/Library/Application Support/codex-autoupdate/codex-autoupdate`. If a real
watcher is installed (the norm on this dogfooding machine once v0.1.1
ships), its KeepAlive agent execs the stub on next restart and the live
installation is broken until reinstalled.

The Go side received exactly this treatment in this revision ("Go
dependencies, not public installation overrides", line 41); the shell side
needs its one-sentence equivalent — e.g. the harness sources the script and
overrides the home-resolution function as an internal test seam, or the
full-script test runs only under a dedicated macOS test user account whose
directory-services home is disposable. Without naming the mechanism, the
promised test is either unrunnable or dangerous, and an implementer will
improvise precisely where the design worked hardest to remove improvisation.

### N1 — nitpick: the bootstrap pin is a movable git tag, not an immutable reference

Line 8/16 pin `raw.githubusercontent.com/.../v0.1.1/install.sh` and line 29
defaults the installed version to the matching tag. A tag can be force-moved,
so the pin authenticates nothing; the round-01 mitigations (main-guard,
inspect-before-run) reduce accident, not tampering. A commit-SHA raw URL, or
a checksum recorded in `llms.txt` next to the command, would make the audited
path immutable at negligible cost. Carried from round 01; unchanged in
substance.

### N2 — nitpick: the script preflight hardcodes `/Applications/ChatGPT.app` while forwarding `--app-path`

Line 24 requires ChatGPT Desktop "at `/Applications/ChatGPT.app`" before
installing anything, but line 26 forwards arbitrary global flags — including
`--app-path` (`cli/root.go:68`) — to the `install` command, and line 35 has
the CLI verify "the configured ChatGPT app path". A user with a relocated
app who passes `--app-path /Volumes/Apps/ChatGPT.app` is rejected by the
script's own hardcoded check even though the CLI would accept the
configuration. Either the script should preflight the forwarded path when
one is supplied, or the design should state that non-default app paths are
unsupported through the bootstrap.

### N3 — nitpick: each release must edit three self-referencing places, and the design leaves the checklist implicit

The tag must contain an `install.sh` whose default version is that same tag
(line 29), and `llms.txt` on `main` must be bumped to the new raw URL (line
8) for the README prompt to reach it. This is workable but easy to get wrong
one release from now; a sentence in the design (or a release checklist in
the repo) naming the three coupled edits would prevent a stale-pin release.

## Requirements coverage

- User correction: fully addressed — one audited agent-first path, no GOBIN
  reasoning, no transient binary, no hidden self-copy; refusal of
  non-canonical invocation is deliberate and now paired with a clear error
  contract and negative-path proof (claim 5).
- Agent protocol: design, worklog, and reviews under `ephemeral/`; proof
  claims strengthened (six claims now include the configured-flags and
  failure paths); post-tag live proof remains inherently deferred and is
  handled the same way prior accepted rounds handled it.
- No over-engineering found: every addition in this revision traces to a
  round-01 finding or the user correction.

## Outcome

material findings remain
