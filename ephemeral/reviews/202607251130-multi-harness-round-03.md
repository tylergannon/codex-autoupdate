# Adversarial review: multi-harness desktop autoupdate, round 3

## Review target

- Commit `3733b68` (`fix: serialize one-shots and recover activation`)
- Baseline `origin/main` at `1623f2b`
- Authoritative target: `ephemeral/projects/codex-autoupdate/multi-harness-plan.md`, including per-harness failure isolation, safe forced equal-version reinstall, and auditable repeated live proof for both applications.

## Evidence inspected

- `AGENTS.md`, the agent protocol, the multi-harness plan, the complete `origin/main...HEAD` changed-file set, and the relevant implementation and test diff with surrounding code.
- CLI and persisted configuration, LaunchAgent construction, shared-lock takeover, coordinator behavior, both activity detectors, release discovery/version comparison, bundle verification, installation, shutdown, readiness, rollback, quarantine, and interrupted-activation recovery.
- Both prior adversarial-review rounds and the remediations for one-shot signaling, process-only idle timing, interrupted activation, and proof overstatement.
- `ephemeral/proof/202607241005-multi-harness/remediation.md` and every referenced live force/rollback, signature, process, inode, quarantine, and fault-injection artifact.
- The currently installed `dev-3733b68` binary and running LaunchAgent, configured with both harnesses and the default shared cache.
- Fresh `go test -count=1 ./...`, `go test -count=1 -race ./...`, `go vet ./...`, and `git diff --check origin/main...HEAD`: all passed.

## Findings

### 1. Issue — a harness that fails during construction still prevents the other harness from running

The contract says a failed harness must not block the other (`multi-harness-plan.md:9,25`), but failure isolation starts only after `settings.watchers` has successfully constructed every watcher. During construction, an interrupted-activation recovery error immediately returns from the whole function (`internal/cli/root.go:490-495`), as does any other application-path inspection error or invalid bundle-path shape (`internal/cli/root.go:501-505`). Because ChatGPT is constructed first by default, any such ChatGPT failure prevents the Claude watcher from being returned or run at all; the same applies in the opposite direction when harness order is explicitly reversed.

The coordinator tests cover a busy watcher and an `Apply` failure only after both watchers already exist (`internal/watch/watch_test.go:120-173`). They do not cover setup/recovery failure. Consequently the continuous LaunchAgent and `update --force` can both violate the promised failure isolation before entering the coordinator. Construction needs to retain a per-harness failed target (or return watchers plus per-target errors) so healthy installed targets are still attempted and the one-shot can join setup failures into its final nonzero result.

### 2. Issue — the required repeated forced-reinstallation proof is still absent, and the claimed automated substitute is not a repeated test

The definition of done explicitly requires live macOS proof that repeatedly forces both applications (`multi-harness-plan.md:27`). The remediation now accurately says its preserved evidence contains only one successful equal-version replacement per harness (`remediation.md:101-112`). Each harness also has a later deliberately failed rollback invocation, but there is no second preserved successful force replacement demonstrating that reinstalling the equal version again remains safe.

The statement that repeated behavior is covered by automated tests is also unsupported. `TestWatcherForceReinstallsEqualVersion` invokes `Step(..., true)` exactly once (`internal/watch/watch_test.go:69-81`); no test repeats a successful force pass against the same watcher/installer state. The downgrade and native-update-race tests exercise different behavior. The existing artifacts convincingly prove one successful force replacement and one controlled rollback for each application, but they do not satisfy the specifically requested repeated-success proof. Preserve another successful installed-command force pass for each real application (and, independently, make the automated repeated claim true or remove it).

## Outcome

material findings remain
