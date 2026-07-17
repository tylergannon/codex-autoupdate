# Adversarial review — installer redesign design doc, round 03

- Date: 2026-07-17 11:15 local
- Reviewer: adversarial-review skill (read-only; only this artifact written)
- Target: revised `ephemeral/projects/codex-autoupdate/installer-design.md`
  (untracked, worktree `installer-redesign`, base `23d8f13`)
- Scope: re-verify every round-02 finding
  (`ephemeral/reviews/202607171110-installer-design-round-02.md`), then
  re-review the full design against the user correction, repository
  instructions, and current implementation

## Evidence inspected

- Full revised design doc (all 59 lines) and the updated worklog
  (`ephemeral/worklog/202607171055-installer-redesign.md`)
- Implementation context re-checked against the new claims:
  `internal/cli/root.go` (flag definitions the shell preflight must mirror,
  `--app-path` only long-form, no shorthand), `internal/launchagent/
  launchagent.go` (seams the promised DI adds), `cmd/codex-autoupdate/
  main.go` (`buildVersion` supports proof claim 4), `internal/watch/watch.go`
- Docs being replaced: `README.md`, `llms.txt`; prior rounds 01–02
- Consistency probe of the release-edit ordering (line 58): the SHA-256
  covers `install.sh` only and is recorded in `llms.txt`, so there is no
  hash-of-self circularity; script content finalizes before hashing, both
  merge together, and the tag points at that exact merge — coherent
- Repo state: design-only (`install.sh`, `RELEASING.md` not yet written);
  only tag is `v0.1.0`

## Round-02 findings — status

- **F1 (shell-bootstrap tests had no safe isolation) — FIXED.** Line 56: the
  harness copies the script without its exact final `main "$@"` invocation,
  sources only function definitions, overrides only `resolve_user_home`, and
  supplies fake `go`/installed commands via `PATH`; production keeps no
  install-root override. This is the shell equivalent of the Go-side DI and
  removes the real-installation clobber risk — with one fail-closed caveat
  (N1 below).
- **N1 (movable tag pin) — FIXED for the primary path.** Line 5: the
  agent-first path now downloads `install.sh`, verifies a SHA-256 digest
  published in `llms.txt`, inspects, then executes — content-pinned even if
  the tag moves. The human short form (line 10) remains tag-pinned as an
  explicitly labeled convenience; that residual is a deliberate, documented
  trade-off and is not re-raised.
- **N2 (hardcoded preflight vs forwarded `--app-path`) — FIXED.** Line 35:
  the preflight resolves `--app-path VALUE` and `--app-path=VALUE` from
  forwarded arguments and checks the configured path; line 56 commits tests
  for both default and forwarded app paths.
- **N3 (implicit three-way release coupling) — FIXED.** Line 58:
  `RELEASING.md` records the coupled edits — version bump, URL updates,
  hash recompute, merge, tag-the-exact-merge, then prove both public paths.

## Findings

No material findings. Genuine nitpicks only.

### N1 — nitpick: the harness's removal of the final `main "$@"` line must fail closed

Line 56 has the harness copy the script "without its exact final `main "$@"`
invocation" and then source it. If a future edit changes that final line
(trailing comment, different quoting, an added line after it), an
exact-match removal silently no-ops and sourcing then *executes* the full
script inside the test — before `resolve_user_home` is overridden — with a
fake `go` on `PATH` writing a stub to the real canonical path: precisely the
round-02 failure the harness exists to prevent. One sentence closes it: the
harness must verify the last line is exactly `main "$@"` and abort the test
run otherwise. (Ordering the override before sourcing does not help, since
sourcing would redefine nothing until `main` has already run.)

### N2 — nitpick: duplicated `--app-path` parsing in shell and cobra can drift

Line 35 introduces a second parser for `--app-path` (space and `=` forms) in
the shell preflight, alongside cobra's (`cli/root.go:68`). Corner semantics
should match deliberately: a repeated flag (cobra: last one wins), and any
future renamed/added path-bearing flag, must be reflected in the preflight or
the script will validate a different path than the CLI configures. Bounded
today (one flag, two forms, tests promised for both), but worth a comment in
the script marking the parser as intentionally mirroring cobra's semantics.

### N3 — nitpick: uninstall's stated contract omits what it leaves behind

Line 43: uninstall "stops the unit and removes the plist and canonical
binary" — matching current `Manager.Uninstall` (`launchagent.go:76-90`).
Logs (`~/Library/Logs/codex-autoupdate/`), the cache directory, and any
quarantine markers (`~/Library/Caches/codex-autoupdate/failed-build-*.json`)
survive. Retention is conventional and arguably desirable for diagnosis, but
since the bootstrap prints the log directory (line 29), the uninstall
message or docs should say these are intentionally kept.

## Requirements coverage

- User correction: fully addressed; unchanged from round 02's assessment,
  now with a content-pinned agent-first path that strengthens the
  "primary installer is an agent" premise.
- Agent protocol: all working material under `ephemeral/`; six proof claims
  cover positive, configured, negative, and teardown paths; pre-release
  isolation is now safely specified on both the Go and shell sides;
  post-tag live proof on this Mac remains and is correctly sequenced last
  in `RELEASING.md`.
- No over-engineering: every addition traces to a review finding or the
  user correction; the design has stayed one page.

## Outcome

only nitpicks remain
