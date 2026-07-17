# Launchd bootstrap retry worklog

proof_failure: The first real `main/install.sh` refresh correctly resolved `@latest` to v0.1.2 but `launchctl bootstrap` raced launchd cleanup after `bootout`, returned error 5, and left the watcher unregistered.
correction: A successful isolated fake-runner path did not prove real launchd refresh timing; post-tag dogfood was decisive and must remain part of every installer release.
decision: Retry bootstrap failures for a bounded five seconds with context-aware 100ms intervals, then retain the loud failure if launchd never accepts the plist.
