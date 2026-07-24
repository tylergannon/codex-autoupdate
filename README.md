# Getting started

Paste this into Codex:

```text
Read https://github.com/tylergannon/codex-autoupdate/blob/main/llms.txt and install locally
```

Or install directly:

```sh
curl -fsSL https://raw.githubusercontent.com/tylergannon/codex-autoupdate/main/install.sh | bash
```

`codex-autoupdate` is a macOS user LaunchAgent that watches OpenAI's stable ChatGPT Desktop appcast. When a newer build is available, it waits until Desktop-managed Codex tasks have been idle for five uninterrupted minutes, then verifies, replaces, and restarts `ChatGPT.app`.

Requirements: macOS, Go, ChatGPT Desktop, and a logged-in administrator account. The watcher first asks ChatGPT to quit normally. If a scheduled-task warning refuses that request after the idle checks pass, it sends `SIGTERM` to the exact ChatGPT main process; it never sends `SIGKILL`. It rolls back an update that does not become ready.

The installer always selects the latest tagged release.

See [llms.txt](llms.txt) for configuration, status, upgrade, uninstall, and recovery instructions.
