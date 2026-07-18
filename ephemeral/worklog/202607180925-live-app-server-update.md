# Live app-server update

decision: The first runtime acceptance gate is that `check --json` finds the live Desktop app-server and reports the currently active Desktop task before any shutdown fix is accepted.
friction: Build 5551 was detected and verified but activation retried 37 times because persistent bundle helpers were treated as proof that the GUI had not exited -> distinguish the application lifecycle process from detached helpers.
friction: The live app-server command is rendered as `codex ... app-server`, while discovery requires the executable path inside ChatGPT.app -> use executable identity or another stable ownership signal instead of argv spelling alone.
correction: Current Desktop uses the app-server holding `~/.codex/app-server-control/app-server-control.sock`; the 2026-07-17 research assumption that Desktop had only a private stdio child is stale.
decision: App-server PID continuity is an activity boundary, not application restart proof; GUI shutdown and readiness follow only `Contents/MacOS/ChatGPT` because crash handlers and task helpers persist independently.
decision: Live build 5551 activation proved the app-server persists across GUI replacement while the GUI PID changes; future restart proof must assert server discoverability plus a new GUI PID, not an app-server PID change.
