# Multi-Harness Desktop Autoupdate

## Contract

- Manage ChatGPT Desktop and Claude Desktop from one user LaunchAgent.
- Enable both harnesses by default, allow explicit harness filtering, and silently skip applications that are not installed.
- Treat only currently executing Codex, Claude Code, or Claude Cowork work as active.
- Give each harness its own uninterrupted idle window and final activity/version preflight.
- Serialize replacements. A busy, missing, or failed harness does not block the other.
- Gracefully quit an idle application, then re-resolve its exact main PID and use `SIGTERM` if quit is refused. Never use `SIGKILL`.
- Verify identity, signing team, strict code signature, Gatekeeper acceptance, architecture, and advertised version before activation.
- Atomically replace, relaunch, check readiness, roll back failures, and quarantine failures per harness/version.

## Forced Reinstallation

`codex-autoupdate update --force` processes both installed harnesses by default.
`--harness chatgpt` and `--harness claude` select one harness. Force mode only
bypasses the newer-version requirement: an equal latest version is reinstalled,
an older version is never installed, and every normal safety check remains in
force.

## Definition of Done

- One LaunchAgent coordinates both harnesses and preserves existing ChatGPT behavior.
- Missing applications are harmless, harness state is reported independently, and failures are isolated.
- Automated tests cover selection, dotted versions, idle behavior, serialization, update races, forced equal-version replacement, downgrade refusal, verification, rollback, quarantine, and failure isolation.
- Live-safe proof demonstrates production release discovery and verified installed-bundle inspection for both applications. Destructive live replacement is only run with explicit operator consent.

