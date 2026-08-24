#!/usr/bin/env bash
# Closeout gap 2 (worklog decision #10, ephemeral/worklog/202608241000-system-instability-audit.md):
# active-status deduplication does not reset across an intervening
# current-state branch. Watcher.Step's "application is current" branch
# (internal/watch/watch.go) resets lastCurrentBuilds but never touches
# lastActiveWork, which is otherwise cleared only when a Step observes idle
# (non-active) work. If the same work is still active when a later,
# genuinely newer candidate appears, the dedup key from an earlier stale
# candidate survives the intervening current cycle untouched and silently
# swallows the log record for the new pending period, even though it
# represents a distinct blocking event against a different candidate. See
# ephemeral/proof/bug-adjudication.md's Bug D fix, which only proved
# dedup within a single unbroken pending run.
#
# Exit status is nonzero on the current production implementation and
# becomes zero only once observing active work, a current-version cycle,
# then the same active work for a newer candidate produces two
# "waiting for active work to finish" status records, not one.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
exec go test -count=1 -v -run '^TestWatcherLogsActiveWorkAgainAfterInterveningCurrentVersionCycle$' ./internal/watch/...
