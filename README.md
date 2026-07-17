# Getting started

Paste this into Codex:

```text
Read https://github.com/tylergannon/codex-autoupdate/blob/main/llms.txt and install locally
```

Or install the official release yourself:

```sh
go install github.com/tylergannon/codex-autoupdate/cmd/codex-autoupdate@v0.1.0
codex-autoupdate install
codex-autoupdate status
```

`codex-autoupdate` is a macOS user LaunchAgent that watches OpenAI's stable ChatGPT Desktop appcast. When a newer build is available, it waits for a continuous period with no running Desktop-managed Codex tasks, then verifies, replaces, and restarts `ChatGPT.app`.

Requirements: macOS, Go 1.26 or newer, ChatGPT Desktop at `/Applications/ChatGPT.app`, and a logged-in administrator account that can write to `/Applications`.

## Use

```sh
codex-autoupdate check --json
codex-autoupdate --idle-window 10m --poll-interval 30m install
codex-autoupdate uninstall
```

Defaults are a five-minute idle window and a fifteen-minute update poll. The watcher never force-kills ChatGPT. It verifies the OpenAI signature and rolls back if the replacement app-server does not become ready.

See [llms.txt](llms.txt) for complete installation and operating instructions. The deferred real-update proof is in [human-proof.md](ephemeral/projects/codex-autoupdate/human-proof.md).
