# Adversarial review: multi-harness desktop autoupdate, round 4

## Review target

- Commit `c576697` (`test: prove repeated forced reinstalls`)
- Baseline `origin/main` at `1623f2b`
- Authoritative target: `ephemeral/projects/codex-autoupdate/multi-harness-plan.md`, including per-harness failure isolation, safe forced equal-version reinstall, and repeated live proof for both applications.

## Evidence inspected

- `AGENTS.md`, the agent protocol, the multi-harness plan, the full current `origin/main...HEAD` changed-file set, and the relevant implementation and test diff with surrounding code.
- CLI/configuration, LaunchAgent persistence, shared-lock takeover, watcher construction, coordinator failure handling, both activity detectors, release discovery/version comparison, bundle verification, shutdown, activation, readiness, rollback, quarantine, and interrupted-activation recovery.
- All three prior multi-harness adversarial-review rounds and the current remediations.
- `ephemeral/proof/202607241005-multi-harness/remediation.md` and every referenced live force/rollback, signature, PID, inode, quarantine, and fault-injection artifact, including the second successful ChatGPT and Claude force-pass transcripts.
- The installed `dev-f23b1a4` runtime (the current production-code commit), its running two-harness LaunchAgent, current application bundle inodes, and absence of a takeover marker or application-bundle residue.
- Fresh `go test -count=1 ./...`, `go test -count=1 -race ./...`, `go vet ./...`, `bash -n install.sh`, and `git diff --check origin/main...HEAD`: all passed.

## Findings

No material findings or genuine nitpicks remain.

The round-three failure-isolation defect is resolved by representing setup/recovery errors as per-harness watcher failures. A one-shot collects that target's error while continuing through other installed targets, and the continuous coordinator logs that target independently without preventing the other watcher from running.

The repeated-proof gap is also resolved. The preserved evidence now shows two successful equal-version replacements for each real application, with changed bundle inodes and main PIDs plus strict signature/team/Gatekeeper validation. The ChatGPT repeat additionally demonstrates real-work deferral and installed-daemon restoration. The automated force test now performs two consecutive successful force passes against the same watcher/installer state.

The earlier fixes for daemon-versus-one-shot lock ownership, fresh idle timing after process-only activity, interrupted-activation recovery, rollback shutdown, and proof accuracy remain intact.

## Outcome

no findings
