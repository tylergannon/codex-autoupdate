# codex-autoupdate

`codex-autoupdate` is a headless macOS user LaunchAgent that safely updates
ChatGPT Desktop and Claude Desktop after their coding-agent work becomes idle.
Both harnesses are enabled by default; applications that are not installed are
skipped.

## Install

Paste this into Codex:

```text
Read https://github.com/tylergannon/codex-autoupdate/blob/main/llms.txt and install locally
```

Or install directly:

```sh
curl -fsSL https://raw.githubusercontent.com/tylergannon/codex-autoupdate/main/install.sh | bash
```

Requirements: macOS, Go, and a logged-in administrator account. At least one of
ChatGPT Desktop or Claude Desktop is useful but neither must be present during
LaunchAgent installation.

## Operate

```sh
binary="$HOME/Library/Application Support/codex-autoupdate/codex-autoupdate"
"$binary" check --json
"$binary" run --once
"$binary" update --force
"$binary" update --force --harness claude
```

`update --force` safely reinstalls the latest official release when it equals
the installed version. It never permits downgrade and retains activity,
signature, identity, readiness, rollback, and quarantine checks.
When the installed LaunchAgent owns the coordinator lock, a one-shot command
requests a cooperative handoff. The watcher finishes its current safe step,
yields the same lock, and launchd resumes it after the command; no second cache
or competing coordinator is used. A concurrent second one-shot command exits
without signaling the one-shot that already owns the lock.

Each harness has its own idle window. Open windows, ordinary chat use, dormant
sessions, and future schedules do not block replacement; currently executing
Codex, Claude Code, or Claude Cowork work does. Once idle, the updater asks the
application to quit. It then waits for every process executing from that app
bundle, including detached helpers. Survivors receive `SIGTERM`, then a bounded
`SIGKILL` fallback; replacement is refused until the old bundle is quiescent.

See [llms.txt](llms.txt) for configuration, LaunchAgent operation, verification,
and recovery.
