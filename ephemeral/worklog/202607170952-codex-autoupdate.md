# Codex autoupdate worklog

decision: Treat ChatGPT Desktop as the product name and com.openai.codex as the macOS bundle and local Codex runtime identity; verified from the installed app bundle.
decision: Observe Desktop task lifecycle from rollout task_started and task_complete records because the Desktop-managed app-server is a private stdio child with no attachable socket.
decision: Stage the full Sparkle enclosure while tasks run, verify the staged OpenAI signature and exact advertised build, then perform a second activity check immediately before graceful quit and atomic replacement.
friction: go mod download did not populate transitive checksums needed by go tool -> bootstrap repositories with go mod download all before invoking tool directives.
correction: run lefthook install explicitly inside the task worktree before its first commit, even though the common Git directory already has a hook.
decision: Cache parsed rollout state by app-server PID, file size, and modification time so five-second activity polling does not repeatedly parse unchanged task histories.
decision: Treat both task_complete and turn_aborted as terminal lifecycle events; interrupted turns do not always emit task_complete before the next turn.
decision: Never force-kill ChatGPT Desktop. A quit timeout aborts the update; a replacement readiness failure restores and relaunches the verified prior bundle.
decision: The real restart proof must run from Terminal against a naturally newer stable appcast build, exercise two deliberate task interruptions of the idle countdown, and retain before/after JSON, watcher logs, signature checks, PIDs, and human UI observations.
review: Claude Fable round 01 identified repeated bad-build retries and bundle residue, missing minimum-system filtering, brittle corrupt-record handling, post-quit relaunch gaps, and direct context cancellation comparison.
decision: Quarantine a failed activation by build in the user cache, remove failed/stale application bundles, and permit either a later release or deliberate marker deletion; this bounds both interruption frequency and disk growth.
decision: Verify the installed bundle before requesting quit and relaunch it after every recoverable post-quit activation failure.
decision: Skip malformed complete rollout records with a surfaced warning while preserving any previously observed task_started state as active until a valid terminal event appears.
review: Claude Fable round 02 re-ran the original repros and focused probes, found every material finding fixed, and concluded no material findings. Remaining notes are bounded/documented nitpicks rather than blockers to the human proof.
