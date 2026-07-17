# codex-autoupdate

`codex-autoupdate` is a macOS per-user watcher for the ChatGPT Desktop app. It downloads a newer OpenAI-signed Desktop build when one appears, waits until the Desktop-managed Codex app-server has had no running tasks for a continuous idle window, then gracefully replaces and restarts the app.

“ChatGPT Desktop” and “Codex” overlap here: the visible application is `ChatGPT.app`, while its macOS bundle identifier and embedded local agent runtime are `com.openai.codex`.

## Requirements

- macOS with ChatGPT Desktop installed at `/Applications/ChatGPT.app`.
- A logged-in administrator account. The watcher is a user LaunchAgent and never invokes `sudo`; administrator group membership normally provides write access to `/Applications`.
- Go 1.26 to build from source.

## Build and inspect

```sh
go build -o codex-autoupdate ./cmd/codex-autoupdate
./codex-autoupdate check
./codex-autoupdate check --json
```

`check` is read-only. It reports the installed and available builds plus any active tasks observed in Desktop rollouts.

## Install the user LaunchAgent

```sh
./codex-autoupdate install
./codex-autoupdate status
```

The installer copies the current executable to `~/Library/Application Support/codex-autoupdate/`, writes `~/Library/LaunchAgents/com.tylergannon.codex-autoupdate.plist`, and bootstraps it in the current GUI login session. Logs go to `~/Library/Logs/codex-autoupdate/`.

The default idle window is five minutes and the default update polling interval is fifteen minutes. Options supplied to `install` are persisted in the LaunchAgent:

```sh
./codex-autoupdate \
  --idle-window 10m \
  --poll-interval 30m \
  install
```

Remove it with:

```sh
./codex-autoupdate uninstall
```

## Safety model

- The full release is downloaded and staged while tasks may still be running.
- The staged app must have the exact appcast version/build, bundle ID `com.openai.codex`, OpenAI team ID `2DC432GLL2`, a strict valid code signature, a passing Gatekeeper assessment, and the host architecture.
- `task_started`, `task_complete`, and `turn_aborted` records from `Codex Desktop` rollouts define task activity. The detector is scoped to the current Desktop app-server process lifetime so a task abandoned by an older crash cannot block updates forever.
- Activity is checked again immediately before shutdown. No public cross-process lock exists to close the final millisecond-scale race completely.
- The app is asked to quit normally. If it does not exit before the timeout, no replacement happens and no process is force-killed.
- The prior signed app bundle is retained until the replacement app-server becomes ready. A failed replacement is removed after the prior app is restored and relaunched. That failed build is quarantined in `~/Library/Caches/codex-autoupdate/failed-build-BUILD.json`; the watcher will automatically try a later build but will not repeatedly restart for the same bad release.

For one foreground check/update cycle, use `./codex-autoupdate run --once`. The executable human restart proof is documented in [`ephemeral/projects/codex-autoupdate/human-proof.md`](ephemeral/projects/codex-autoupdate/human-proof.md).
