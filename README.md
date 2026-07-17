# Getting started

Paste this into Codex:

```text
Read https://github.com/tylergannon/codex-autoupdate/blob/main/llms.txt and install locally
```

Or install directly:

```sh
curl -fsSL https://raw.githubusercontent.com/tylergannon/codex-autoupdate/v0.1.1/install.sh | bash
```

`codex-autoupdate` is a macOS user LaunchAgent that watches OpenAI's stable ChatGPT Desktop appcast. When a newer build is available, it waits until Desktop-managed Codex tasks have been idle for five uninterrupted minutes, then verifies, replaces, and restarts `ChatGPT.app`.

Requirements: macOS, Go, ChatGPT Desktop, and a logged-in administrator account. The watcher never force-kills ChatGPT and rolls back an update that does not become ready.

See [llms.txt](llms.txt) for configuration, status, upgrade, uninstall, and recovery instructions.
