# Adversarial review — installer redesign design doc, round 01

- Date: 2026-07-17 11:00 local
- Reviewer: adversarial-review skill (read-only; only this artifact written)
- Target: `ephemeral/projects/codex-autoupdate/installer-design.md` (untracked,
  worktree `installer-redesign`, base `23d8f13` "docs: prepare public v0.1.0
  release")
- User request under review (recorded in
  `ephemeral/worklog/202607171055-installer-redesign.md`): "The primary
  installer is an agent; do not make the human reason about `GOBIN`, resolve a
  transient Go binary, and then accept a hidden self-copy."

## Evidence inspected

- Design doc: `ephemeral/projects/codex-autoupdate/installer-design.md`
- Repository instructions: `/Users/tyler/src/CLAUDE.md` (not applicable —
  Svelte MCP guidance), `AGENTS.md`, agent protocol
  (`~/.agents/skills/agent-protocol/SKILL.md`)
- Current installer implementation: `internal/cli/root.go` (install/uninstall/
  status commands, flag plumbing), `internal/launchagent/launchagent.go`
  (self-copy, plist render, launchctl bootstrap/kickstart, `Manager.paths()`,
  `requireAdminUser`), `cmd/codex-autoupdate/main.go` (`buildVersion` via
  `debug.ReadBuildInfo`)
- Runtime behavior the install feeds: `internal/watch/watch.go` (cycle error
  handling when the app is absent), `internal/update/installer.go` (Apply
  rename sequence), `internal/runlock/lock.go`
- Documentation the design replaces: `README.md`, `llms.txt`
- Prior rounds: `ephemeral/reviews/202607171010-...-round-01.md`,
  `202607171030-...-round-02.md`; proof material `human-proof.md`,
  `live-proof.sh`
- Repo state: only tag is `v0.1.0`; design pins `v0.1.1` (a future,
  self-referencing tag — coherent as a plan, noted under F4)

## Findings

### F1 — issue (incorrect/incomplete design): `CODEX_AUTOUPDATE_INSTALL_DIR` and the isolated-home tests contradict the canonical-binary refusal, and no override mechanism is specified

Design lines 22 and 26–32: the script honors `CODEX_AUTOUPDATE_INSTALL_DIR`
"for reproducible testing and controlled installation", while
`codex-autoupdate install` "verifies that the executing file is the canonical
installed binary" and "direct invocation from a build tree or arbitrary
`GOBIN` fails". These two rules collide: if the script installs into any
non-default directory and then (step 3, line 19) invokes that binary's
`install`, the binary is by definition not at the canonical path and must
refuse — the design's own controlled-install path cannot complete as written.

The same gap breaks the promised proof: line 41 says pre-release integration
tests "use an isolated home". Today the canonical path is derived inside
`launchagent.Manager.paths()` (`launchagent.go:113-138`) from
`user.Current().HomeDir`, and the CLI never exposes `Manager.HomeDir`/`UID`
(`cli/root.go:173`, `launchagent.Manager{}`); `user.Current()` caches and, in
cgo builds on darwin, does not follow a `$HOME` override, so even
`HOME=/tmp/x codex-autoupdate install` does not relocate the canonical path.
The design must specify the single source of truth the script, the CLI check,
and the tests all share (e.g. the CLI honors the same env var, or an explicit
`--install-root` used only by tests), otherwise proof claim "unit/integration
tests … exercise plist creation, refresh, and removal" is unimplementable
without ad-hoc mechanisms the design forbids elsewhere.

Impact: the two named escape hatches (testing, controlled install) fail
against the refusal rule; whoever implements this must invent an unspecified
contract, which is exactly what a design doc exists to pin down.

### F2 — issue (incomplete requirement): the bootstrap drops the documented prerequisite that ChatGPT Desktop is installed, producing a "successful" install of a permanently failing agent

Design line 17: `install.sh` "requires macOS and `go`" — nothing else. The
contract being replaced (`llms.txt:7-14`) makes the installer verify
`/Applications/ChatGPT.app` exists and the user is an admin before touching
anything. Under the redesign, admin is still enforced at install time
(`launchagent.go:140-157` `requireAdminUser`), but no layer checks that
ChatGPT Desktop exists: `settings.validate` (`cli/root.go:254-265`) only
validates path shape, and the first existence check happens inside the
running watcher (`watch.go:67-69`), which logs "watch cycle failed" and
retries forever every poll interval (`watch.go:55-62`).

Reproduction (per design): run the bootstrap on a Mac without ChatGPT
Desktop. Every step succeeds, the script prints the success summary (line
20), and a KeepAlive LaunchAgent is left failing every 15 minutes
indefinitely. Proof claim 1 (line 36, "starts a readable LaunchAgent") passes
on this Mac and never catches it. The design should require the bootstrap (or
the `install` command) to check `/Applications/ChatGPT.app` — or explicitly
record the decision not to.

### F3 — issue (incomplete requirement): flag forwarding is a named behavior but no proof claim or test exercises it, and the one audited command cannot carry flags

Design line 19 makes forwarding "optional global CLI flags supplied after
`bash -s --`" a numbered bootstrap behavior, and the current contract
documents configured installs (`llms.txt:51-60`, persisted into plist
`ProgramArguments` via `renderPlist`, `launchagent.go:212-240`). Yet the
proof claims (lines 36-41) cover only default install, re-run, version/check,
and uninstall — nothing verifies that `curl … | bash -s -- --idle-window 10m`
lands `--idle-window 10m` in the plist. Note also the only command the design
publishes (line 8) is `curl -fsSL … | bash`, which cannot accept arguments;
an agent following the single audited path has no documented way to pass the
flags the feature exists for. Either add a proof claim plus the `bash -s --`
form to the user-facing contract, or drop behavior 3.

### F4 — nitpick: the pinned bootstrap URL is a mutable tag piped straight to bash

Line 8 pins `raw.githubusercontent.com/...//v0.1.1/install.sh`. A git tag is
movable, so the pin is not an integrity guarantee — the fetched script and
the `@v0.1.1` it installs can both change silently; a commit-SHA raw URL (or
a checksum in `llms.txt`) would be immutable. Separately, an unguarded
`curl | bash` executes a truncated script on a dropped connection; the
standard mitigation (define `main` and call it on the last line) costs
nothing. Both are hygiene points for a script whose stated audience is an
unattended agent.

### F5 — nitpick: "idempotent" re-run refreshes via `bootout`/`kickstart -k`, which can kill a watcher mid-update

Line 11 invites re-running the bootstrap at any time. The refresh path
(design line 26; today `launchagent.go:64-72`) boots the running agent out
and kickstarts it. If the watcher is inside `Apply`'s rename window
(`update/installer.go:152-162`, between moving `ChatGPT.app` to the backup
path and activating the staged bundle), a kill in that window leaves
`/Applications` with no `ChatGPT.app` and the backup as a hidden dotfile that
`cleanupResidue` does not sweep (it globs only `*.new` and `failed-*`,
`installer.go:382-403`). The window is milliseconds wide and SIGTERM is
handled gracefully (`main.go:17`), so this is a nitpick — but the design
could cheaply require the refresh to respect the run lock or wait for a
graceful exit before `kickstart -k`.

## Requirements coverage (for completeness)

- User correction (agent-first install, no GOBIN reasoning, no transient
  binary, no hidden self-copy): addressed directly and well — single audited
  path, `go install` straight into the canonical location, `install` reduced
  to launchd registration. The refusal of arbitrary-GOBIN invocation is a
  deliberate, defensible consequence, not over-engineering.
- Agent protocol: design and worklog live under `ephemeral/`, proof-before-
  merge-ready is planned (line 41); the post-tag live proof is inherently
  deferred because the artifact under proof is the tag itself — acceptable
  and consistent with how prior rounds treated deferred proof.
- Version-reporting proof claim 3 is sound: `go install …@v0.1.1` stamps
  `debug.ReadBuildInfo().Main.Version`, which `buildVersion()`
  (`main.go:30-39`) surfaces.
- The "atomic" upgrade claim (line 29) is acceptable on macOS: `go install`
  renames or remove-then-creates, so a running agent keeps its old inode.

## Outcome

material findings remain
