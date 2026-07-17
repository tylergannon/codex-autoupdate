# Installer redesign

## User-facing contract

The primary installer is an agent. The README therefore starts with one prompt that tells the agent to read `llms.txt` and install the watcher. `llms.txt` tells the agent to download `install.sh`, verify its published SHA-256 digest, inspect it, and execute it. This makes the agent path content-pinned even if a git tag is moved.

For a human who explicitly prefers the conventional short form, the README also offers:

```sh
curl -fsSL https://raw.githubusercontent.com/tylergannon/codex-autoupdate/v0.1.1/install.sh | bash
```

The same command is suitable for a human. It is idempotent and also upgrades an existing installation.

Configuration is optional and remains a single command:

```sh
curl -fsSL https://raw.githubusercontent.com/tylergannon/codex-autoupdate/v0.1.1/install.sh | \
  bash -s -- --idle-window 10m --poll-interval 30m
```

## Bootstrap behavior

`install.sh`:

1. requires macOS, `go`, an administrator user, and ChatGPT Desktop at `/Applications/ChatGPT.app` before installing anything;
2. resolves the logged-in user's home through macOS directory services, then installs the tagged command directly into `~/Library/Application Support/codex-autoupdate/codex-autoupdate` with `GOBIN`, without a temporary or duplicate user-facing binary;
3. invokes that canonical binary's `install` command, forwarding optional global CLI flags supplied after `bash -s --`;
4. prints the installed version, stable path, status command, log directory, and uninstall command.

The script defaults to its matching release, `v0.1.1`. There is no install-directory override: the script, CLI, plist, and uninstaller share one canonical location. `CODEX_AUTOUPDATE_VERSION` exists only to test or deliberately preview a version.

The script defines `main` and invokes it only on the final line so a truncated `curl` response cannot execute a partial program. The release-tag URL is human-readable and versioned; `llms.txt` publishes the script content hash for the agent-first path.

The preflight resolves `--app-path VALUE` and `--app-path=VALUE` from forwarded arguments, defaulting to `/Applications/ChatGPT.app`, and checks that configured path before installation.

## CLI and LaunchAgent behavior

`codex-autoupdate install` stops copying its own executable. It independently verifies the administrator user, configured ChatGPT app path, and that the executing file is the canonical installed binary. It writes the plist pointing at that exact file, gracefully replaces any existing LaunchAgent, bootstraps it, and kickstarts without `-k` so an already starting process is not killed a second time. This keeps responsibility crisp:

- `go install` installs or upgrades the binary atomically;
- `codex-autoupdate install` registers or refreshes launchd state;
- `codex-autoupdate uninstall` stops the unit and removes the plist and canonical binary.

Direct invocation from a build tree or arbitrary `GOBIN` fails with a message directing the user to `install.sh`, preventing fragile LaunchAgents that point into disposable locations. Internal Manager integration tests inject an isolated home, UID, admin check, and fake launchctl runner; these are Go dependencies, not public installation overrides.

## Proof claims

1. Running the bootstrap from a clean state installs one binary at the canonical path, writes a plist pointing to it, and starts a readable LaunchAgent.
2. Re-running the bootstrap upgrades or refreshes the same path and service without creating a second binary.
3. Flags passed through `bash -s --`, including a non-default idle window and poll interval, appear in the installed plist.
4. The installed tagged binary reports the release version and can perform a read-only update check.
5. Installing without ChatGPT Desktop or from a non-canonical executable fails before registering a LaunchAgent.
6. Uninstall stops the service and removes both the plist and canonical binary.

Before release, Manager integration tests use an isolated home and fake launchctl to exercise canonical-path validation, plist creation with forwarded configuration, refresh, and removal. The shell harness copies the script without its exact final `main "$@"` invocation, sources the remaining function definitions, overrides only `resolve_user_home` in the harness, and supplies fake `go`/installed commands through `PATH`; production has no install-root override. Tests cover both default and forwarded app paths. After release, the tagged script is run on this Mac, and the real installed path, launchd status, plist arguments, version, and read-only `check` output are captured. The final installation remains enabled unless proof exposes a defect.

`RELEASING.md` records the coupled release edits: bump the default version in `install.sh`, update versioned URLs in README/llms.txt, recompute the script SHA-256 in `llms.txt`, merge, tag that exact merge, and prove the public content-pinned agent path plus short human path.
