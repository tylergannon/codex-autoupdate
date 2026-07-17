# Launchd refresh proof

## Claim

Refreshing an existing installation tolerates the transient launchd error-5 window after `bootout`, re-registers the watcher, and leaves the latest tagged binary running.

## Pre-release evidence

`TestManagerInstallRefreshStatusAndUninstall` injects the exact observed first-bootstrap failure (`Bootstrap failed: 5: Input/output error`). The Manager retries, succeeds on the second bootstrap, kickstarts once, and then completes another ordinary refresh before proving status and uninstall boundaries.

## Post-tag requirement

Tag the merged hotfix without changing `install.sh`, README, or llms.txt. Run the unchanged public `main/install.sh`; it must resolve the new tag through `@latest`, upgrade the installed binary, and leave `com.tylergannon.codex-autoupdate` running. Rerun once more to exercise the real refresh path.
