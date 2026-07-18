# Research findings

Research completed on 2026-07-17 against the installed ChatGPT Desktop app and Codex CLI before implementation.

## 2026-07-18 runtime correction

- Current Desktop tasks run through the long-lived app-server that owns `~/.codex/app-server-control/app-server-control.sock`; its executable may be rendered by `ps` as simply `codex`, so the control-socket owner is the stable process identity.
- The app-server and detached crash/task helpers can outlive the GUI application. Update shutdown and readiness must therefore follow only `Contents/MacOS/ChatGPT`, while activity polling continues to follow the app-server control socket.

## Product and process identity

- `/Applications/ChatGPT.app` displays as ChatGPT but has bundle identifier `com.openai.codex`.
- The installed short version is `26.715.21425`; its numeric bundle build is `5488`.
- The app is signed by `Developer ID Application: OpenAI OpCo, LLC (2DC432GLL2)` and carries a stapled notarization ticket.
- The Desktop app launches `Contents/Resources/codex ... app-server` as a stdio child. Its standard streams are anonymous Unix pipes; it does not expose a filesystem socket or network listener.
- The public `codex app-server` command can expose WebSocket or Unix-socket transports, but starting another app-server would create another process and would not expose the Desktop process's in-memory active-turn state.

## Update source and behavior

- The app embeds Sparkle and its packaged `app.asar/package.json` declares `codexSparkleFeedUrl` as `https://persistent.oaistatic.com/codex-app-prod/appcast.xml`.
- The live appcast's newest compatible item matches build `5488` and supplies a full arm64 ZIP enclosure, byte length, Sparkle EdDSA signature, minimum macOS version, and deltas.
- The app's own updater uses a native Sparkle addon behind Electron IPC. There is no supported external CLI for that private IPC.
- The stable Codex CLI embeds `https://persistent.oaistatic.com/codex-app-prod/Codex.dmg` for first-time Desktop installation, but `codex app` only opens an already installed app. It does not promise to update it.
- `/Applications` is `root:admin` mode `0775`, and the current app is owned by the logged-in admin user. A user LaunchAgent can replace it without interactive sudo on this machine. Installation must reject a non-admin user when the configured application path is not writable.

## Activity signal

- Desktop session rollouts identify `originator: "Codex Desktop"` in `session_meta`.
- Every observed running turn begins with `event_msg.payload.type == "task_started"` and every observed terminal turn ends with `event_msg.payload.type == "task_complete"`, including interrupted/resumed task histories.
- The shared state database does not persist current turn status. Rollout lifecycle records are therefore the strongest cross-process signal available without unsupported process injection.
- To avoid treating a turn abandoned by an earlier app crash as active forever, only Desktop rollouts written since the current Desktop app-server process start are candidates.
- There is no cross-process lock that prevents a new task from starting in the milliseconds between the final activity read and application quit. The updater must recheck immediately before quit, log this limitation, and expose a human proof procedure.

## Chosen update transaction

1. Poll the stable appcast and compare its numeric build against `CFBundleVersion`.
2. Download the full enclosure while tasks may still be active.
3. Extract it with `ditto`; verify exact bundle ID, advertised build/version, strict code signature, OpenAI team ID, and Gatekeeper assessment.
4. Track a continuous configurable idle window, resetting whenever any Desktop rollout is active.
5. Recheck activity immediately before shutdown.
6. Ask the app to quit normally and abort rather than force-kill if it does not exit in time.
7. copy the staged bundle onto the `/Applications` volume, rename the old and new bundles atomically, and retain the old bundle until the replacement application starts.
8. Launch the replacement. Roll back and relaunch the prior bundle if readiness is not observed.

## Official product references

- Codex app-server command and transports: https://learn.chatgpt.com/docs/developer-commands?surface=cli#cli-codex-app-server
- Codex app installer command: https://learn.chatgpt.com/docs/developer-commands?surface=cli#cli-codex-app
- Codex state locations and environment variables: https://learn.chatgpt.com/docs/config-file/reference

The exact Desktop updater IPC and rollout-file lifecycle are not public contracts; the implementation will isolate both behind small interfaces and cover their current behavior with fixtures.
