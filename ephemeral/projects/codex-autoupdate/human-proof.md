# Human-executable live proof

This proof is intentionally deferred until the stable appcast advertises a build newer than the installed ChatGPT Desktop build. Run it from Terminal, not from ChatGPT Desktop, because the application under test will terminate this task and restart itself.

## Preconditions

1. Build the exact reviewed commit and keep Terminal open:

   ```sh
   git status --short
   git rev-parse HEAD
   go build -trimpath -o ./codex-autoupdate ./cmd/codex-autoupdate
   ./codex-autoupdate check --json
   ```

2. Confirm `available.Build` is greater than `installed.Build`. If it is not, stop; the real restart path cannot be proved without fabricating an update.
3. Finish or checkpoint every important Desktop task. The proof deliberately exercises activity waiting and then restarts ChatGPT Desktop.
4. Ensure no installed watcher is already running (`./codex-autoupdate status` should report that the service is absent), and ensure `jq` is available.

## Execute

From the repository worktree, run:

```sh
CODEX_AUTOUPDATE_LIVE_PROOF=restart-chatgpt \
  ephemeral/projects/codex-autoupdate/live-proof.sh ./codex-autoupdate
```

The script records its evidence under a new timestamped directory in `ephemeral/proof/` and starts one real update cycle with a one-minute idle window.

While the script says it is waiting for idle:

1. Start a harmless Desktop task such as “wait 20 seconds, then say done.”
2. Confirm the proof log names that thread as active.
3. Let the task complete.
4. During the first idle countdown, start one more harmless task. Confirm the countdown restarts from that task's terminal lifecycle time.
5. Let every task finish and do not start another for one minute.

ChatGPT Desktop should quit and restart. The Terminal script must survive because it is not a child of the app.

## Passing evidence

The script exits zero only if all of these are true:

- the preflight appcast build was newer than the installed build;
- the foreground watcher exited successfully;
- the installed build increased to exactly the preflight appcast build;
- the restarted Desktop app-server has a different PID;
- the final app passes strict code-signature and Gatekeeper verification;
- the watcher log contains the idle wait, graceful shutdown, and completed update events.

The human additionally verifies that the two deliberate tasks reset the idle window and that pre-existing task history remains visible after restart. Those UI observations should be written into `human-observations.txt` in the generated proof directory.

## Failure handling

If the updated app does not become ready, the watcher is expected to restore and relaunch the prior bundle, write `~/Library/Caches/codex-autoupdate/failed-build-BUILD.json`, and exit nonzero. Preserve the proof directory, that small quarantine marker, and `~/Library/Logs/com.openai.codex/` for diagnosis. The same build will not be attempted again unless the marker is deliberately removed; a later build remains eligible automatically.
