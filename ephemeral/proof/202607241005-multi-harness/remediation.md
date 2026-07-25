# Multi-harness remediation proof

Proof head: the commit containing this file.
Status: complete for the observable runtime claims in the multi-harness plan.

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

## Live forced replacement

The installed `dev-f8eadec` binary ran equal-version forced reinstallations
against both real applications.

ChatGPT build 5848 was replaced by another verified build 5848. The original
bundle inode `250180315` became `250207281`, the main process changed from PID
28608 to PID 30279, and the relaunched application passed strict signature,
team `2DC432GLL2`, and Gatekeeper checks. Graceful quit timed out, so the
updater re-resolved exact PID 28608, sent `SIGTERM`, confirmed exit, and
continued without `SIGKILL`.

Claude build 1.24012.9 was replaced by another verified build 1.24012.9. The
bundle inode changed from `250225566` to `250231443`, the main process changed
from PID 31105 to PID 31861, and the relaunched application passed strict
signature, team `Q6L2SF6YDW`, and Gatekeeper checks.

The raw before, updater, and after records are:

- `live-chatgpt-force-once.pre.txt`
- `live-chatgpt-force-once.log`
- `live-chatgpt-force-once.post.txt`
- `live-claude-force.pre.txt`
- `live-claude-force.log`
- `live-claude-force.post.txt`

Additional equal-version passes also completed successfully for both
applications, demonstrating that repeated `--force` use remains operational.

## Live rollback behavior

Both controlled failures ran against the real `/Applications` installation,
not a disposable application copy.

For Claude, the proof terminated only the newly launched replacement PID 36217.
Readiness timed out after three seconds, the updater restored the original
bundle inode `250231443`, relaunched it as PID 36270, wrote the per-version
quarantine record, and left no backup residue. The restored app passed strict
signature, team, and Gatekeeper checks.

For ChatGPT, the fault injector removed execute permission from only the
activated replacement bundle (inode `250286216`). Launch failed, the updater
restored the original bundle inode `250257054`, relaunched it as PID 45530,
wrote the per-version quarantine record, and left no backup residue. The
restored app passed strict signature, team, and Gatekeeper checks.

The proof quarantine records were inspected and then removed so the installed
watcher was not left suppressing an otherwise valid current release. The
failed staged bundles and temporary proof LaunchAgents were also removed.

The raw rollback records are:

- `live-claude-rollback.pre.txt`
- `live-claude-rollback-fault.log`
- `live-claude-rollback.log`
- `live-claude-rollback.post.txt`
- `live-chatgpt-rollback2.pre.txt`
- `live-chatgpt-rollback2-fault.log`
- `live-chatgpt-rollback2.log`
- `live-chatgpt-rollback2.post.txt`

## Automated rollback behavior

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
