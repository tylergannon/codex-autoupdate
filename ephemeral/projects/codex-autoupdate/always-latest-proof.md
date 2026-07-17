# Always-latest installer proof

## Claims

1. The public bootstrap has no pinned release and no version override; every invocation asks Go for `@latest`.
2. README and agent instructions use the stable `main/install.sh` URL, so ordinary releases require no installer documentation edits.
3. Running the corrected public bootstrap after tagging installs the newly tagged release over the existing watcher and leaves the LaunchAgent running.

## Pre-merge evidence

`TestInstallScriptAlwaysInstallsLatestAndForwardsFlags` executes the real installer function body in an isolated home. Its fake Go command records the exact request and requires `install github.com/tylergannon/codex-autoupdate/cmd/codex-autoupdate@latest`; the test also rejects any reintroduction of `CODEX_AUTOUPDATE_VERSION`.

Repository assertions reject semantic-version strings in active installer surfaces, require the stable main URL, and verify that the SHA-256 published in `llms.txt` matches `install.sh`.

Post-tag public installation evidence will be added to the release PR after the new tag exists.
