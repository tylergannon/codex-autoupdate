# Always-latest installer worklog

correction: The installer must always select the latest tagged release; a pinned default or optional version override still creates release bookkeeping and violates the intended contract.
decision: Fetch the stable `main/install.sh` URL and make the script unconditionally invoke `go install github.com/tylergannon/codex-autoupdate/cmd/codex-autoupdate@latest`.
decision: Ordinary releases never edit installer or documentation version strings. Recompute the agent-path checksum only when the installer script itself changes.
