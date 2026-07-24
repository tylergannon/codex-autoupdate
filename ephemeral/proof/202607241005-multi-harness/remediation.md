# Multi-harness remediation proof

Proof head: `f8eadec` plus the proof/test-only follow-up containing this file.
Status: in progress; completion is not claimed while live activation evidence
listed under "Remaining" is absent.

## Cooperative one-shot takeover

The `f8eadec` binary was installed as the real
`com.tylergannon.codex-autoupdate` LaunchAgent with both harnesses enabled. The
daemon owned the default lock as PID 90040. The installed binary then ran the
one-shot force command against deliberately missing application paths while
retaining the real default cache:

```text
before_launchagent_pid=90040
one_shot_exit=0
takeover_marker=absent
restarted_launchagent_pid=90729 lock_owner=90729
```

This demonstrates that the documented command path no longer fails on the
daemon's lock, does not use a second cache, cleans its request marker, and lets
launchd resume a single coordinator holding the original lock.

An isolated whole-binary failure-path probe also ran a continuous daemon against
an invalid Claude bundle, requested takeover from a one-shot command, and
observed:

```text
daemon_pid=89493 lock_pid=89493
one_shot_exit=1 daemon_exit=0 takeover_marker=absent
```

The one-shot retained its expected bundle-inspection error, while the daemon
yielded cleanly and the request marker was removed on failure.

## Live activity and verified staging

Production discovery found new releases for both installed applications:

```text
chatgpt installed=5828 available=5848 active=019f94b2-62d0-76f0-a76b-004e2b52bf47
claude installed=1.24012.1 available=1.24012.9 active=claude-task-pid:17911,claude-task-pid:17914
```

The real LaunchAgent staged both releases, then independently deferred both
activations:

```text
update staged and verified harness="ChatGPT Desktop" build=5848
waiting for active work to finish harness=chatgpt work=[019f94b2-62d0-76f0-a76b-004e2b52bf47]
update staged and verified harness="Claude Desktop" build=1.24012.9
waiting for active work to finish harness=claude work="[claude-task-pid:17911 claude-task-pid:17914]"
```

The Claude PIDs are a still-running Claude Code-launched `npm test` process and
its `tail` process. Their task-output path has been deleted, but their open
standard streams remain visible through `lsof`; the pre-remediation detector
incorrectly reported this state idle.

Both staged production bundles passed independent inspection:

```text
ChatGPT build=5848 bundle=com.openai.codex team=2DC432GLL2
ChatGPT strict codesign=valid Gatekeeper="Notarized Developer ID"
Claude build=1.24012.9 bundle=com.anthropic.claudefordesktop team=Q6L2SF6YDW
Claude strict codesign=valid Gatekeeper="Notarized Developer ID"
```

## Rollback behavior

The focused macOS installer integration tests use real temporary bundle
directories and atomic filesystem renames while replacing macOS process/signing
commands with deterministic fixtures. A running replacement was made to fail
readiness at `1ns`. The proof observed graceful quit refusal, exact PID 123
`SIGTERM`, restoration of build 1, relaunch, quarantine, and removal of the
rollback bundle. A second case refused `SIGTERM` and proved that neither the
running build-2 bundle nor the retained build-1 backup was moved.

```text
PASS TestRollbackTerminatesRunningFailedReplacementBeforeRestore
PASS TestRollbackDoesNotMoveBundlesWhenFailedReplacementCannotStop
```

## Automated checks

Fresh checks after the runtime fixes:

```text
go test -count=1 ./...             PASS
go test -count=1 -race ./...       PASS
go vet ./...                       PASS
golangci-lint run ./...            0 issues
bash -n install.sh                 PASS
```

## Remaining

- Observe the staged ChatGPT build 5848 replace build 5828, relaunch, and pass
  signed readiness after the current Codex task becomes idle.
- Resolve or explicitly preserve the two live Claude task processes, then
  observe Claude build 1.24012.9 replace 1.24012.1 and pass signed readiness.
- Run a second equal-version forced reinstall for each application through the
  installed one-shot command.
- A real installed-app readiness failure is not yet induced; the controlled
  rollback proof above is at the installer integration boundary.
