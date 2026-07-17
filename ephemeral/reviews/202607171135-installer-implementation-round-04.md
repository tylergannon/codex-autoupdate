# Adversarial review — installer implementation, round 04

- Date: 2026-07-17 11:35 local
- Reviewer: adversarial-review skill (read-only; only this artifact written)
- Target: implementation at `b9f6539` ("feat: add agent-first installer") on
  worktree `installer-redesign`, plus the uncommitted
  `installer-proof.md` checkpoint update (only working-tree change)
- Reviewed against: accepted design
  (`installer-design.md`, round-03 outcome "only nitpicks remain"), the user
  correction (worklog line 1), and repository/agent-protocol instructions

## Evidence inspected

- Full diff of `b9f6539` and current contents of every changed file:
  `install.sh`, `internal/launchagent/launchagent.go`,
  `internal/launchagent/launchagent_test.go`,
  `internal/launchagent/install_script_test.go`, `internal/cli/root.go`
  (message/help-text changes only — verified the diff is exactly that),
  `README.md`, `llms.txt`, `RELEASING.md`, `installer-proof.md`, worklog
- Surrounding code re-checked for interaction regressions:
  `cmd/codex-autoupdate/main.go` (`buildVersion` supports the post-tag
  version claim), `internal/watch/watch.go`, `internal/update/installer.go`
- Verification performed (read-only; no project files touched):
  - `go build ./...`, `go vet ./...`, `go test -race ./...` and forced
    `-count=1 -v` on `internal/launchagent` — all 6 tests pass
  - `golangci-lint run ./...` — 0 issues; `git diff --check` — clean;
    `bash -n install.sh` — OK
  - `shasum -a 256 install.sh` = `a6789cb1…6331`, byte-identical to the
    digest published in `llms.txt:15` — the content-pinned agent path is
    internally consistent, and squash-merging does not invalidate it
    (content, not commit, is hashed; `RELEASING.md` steps 3/5 order the
    recompute before tagging the exact merged commit)
  - Hand-traced `install.sh`: `set -euo pipefail` with
    `app_path=$(app_path_from_args "$@")` on its own line (exit status not
    masked by `local`), last-one-wins `--app-path` parsing matching cobra,
    `die` propagation out of command substitution, `main "$@"` only on the
    final line, `GOBIN` with the space-containing path correctly quoted
  - Confirmed the harness fails closed: `installerFunctionBody` rejects any
    script whose final line is not exactly `main "$@"`
    (`install_script_test.go:147-154`, covered by
    `TestInstallerFunctionBodyFailsClosed`) — round-03 N1 implemented
  - Confirmed round-03 N2 (alignment comment at `install.sh:15`; divergence
    directions all fail loudly — see N2 below) and N3 (uninstall message
    `cli/root.go` "logs and cache retained"; `llms.txt:48` documents what is
    kept and how to remove it)
- Design conformance: every design statement maps to implementation —
  preflight order (`install.sh:41-52`, proven to run before `go install` by
  `TestInstallScriptRejectsMissingAppBeforeGoInstall`), directory-services
  home resolution (`install.sh:32-39`), no install-root override, canonical
  binary enforced by inode comparison (`os.SameFile`,
  `launchagent.go:195-211` — stronger than the path comparison the design
  implied, and immune to firmlink/symlink spelling), self-copy removed,
  graceful `bootout` → `bootstrap` → non-forcing `kickstart`
  (`launchagent.go:77-83`), DI seams as Go dependencies only
  (`Manager.Runner`, `Manager.RequireAdmin`), all six proof claims traced to
  tests or the documented post-tag step

## Findings

No material findings. Genuine nitpicks only.

### N1 — nitpick: non-forcing `kickstart` semantics on an already-started service are only proven against a fake runner

`Manager.Install` treats any `launchctl kickstart` failure as fatal
(`launchagent.go:81-83`). After `bootstrap` of a `RunAtLoad` job, launchd
may already have started the process; if real `launchctl` returns non-zero
for a kickstart of an already-running service on this macOS version, a
fully successful registration would be reported as a failure. The fake
runner (`launchagent_test.go:157-168`) cannot decide this; the post-tag
live proof (proof claim 1) will. If it surfaces, tolerate an
"already running"-class kickstart result rather than failing the install.

### N2 — nitpick: pathological flag orderings diverge between the shell preflight and cobra, though every divergence fails loudly

`app_path_from_args` (`install.sh:12-30`) does not know which *other* flags
take separate values. `bash -s -- --codex-home --app-path=/x` makes the
script validate `/x` while cobra would consume `--app-path=/x` as the value
of `--codex-home`; the CLI then rejects it (`--codex-home` must be
absolute, `cli/root.go:258-259`), so nothing inconsistent is installed —
but the error the user sees is about a flag they didn't intend. Bounded,
commented (`install.sh:15`), and fail-closed in every traced ordering;
worth remembering if global flags are ever added or renamed.

### N3 — nitpick: a `bootstrap` failure leaves the previous LaunchAgent booted out

`Install` writes the new plist and boots the old job out before
`bootstrap` (`launchagent.go:72-80`). If `bootstrap` then fails, the
watcher is unregistered until the user reruns the installer — the error is
loud and rerun-able, and the behavior predates this change, but a
bootstrap-failure path that re-bootstraps the previous plist would make
refresh strictly non-destructive.

## Requirements coverage

- User correction: implemented end-to-end — the agent path in `llms.txt` is
  a verify-inspect-execute procedure with a content hash; the human path is
  one command; no GOBIN reasoning, no transient binary, no self-copy;
  non-canonical invocation fails with the documented pointer (verified in
  `TestManagerInstallRejectsNoncanonicalExecutableAndMissingApp` and, per
  the proof doc, once against the real CLI/launchctl on this Mac).
- Agent protocol: work in a worktree, worklog appended, checkpoint commit
  made, proof artifact updated to name the proved checkpoint; the required
  checks listed in `installer-proof.md` all re-verified here independently.
  Post-tag live proof is correctly the only deferred item and is sequenced
  in `RELEASING.md`.
- No over-engineering: every addition traces to the design or a review
  round; `internal/cli` remains message-level changes only.

## Outcome

only nitpicks remain
