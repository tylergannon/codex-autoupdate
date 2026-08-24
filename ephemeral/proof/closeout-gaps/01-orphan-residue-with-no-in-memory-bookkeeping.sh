#!/usr/bin/env bash
# Closeout gap 1 (worklog decision #10, ephemeral/worklog/202608241000-system-instability-audit.md):
# installing the fixed binary cannot remove an already-orphaned staged bundle
# left by a prior process, because the persisted staged path is no longer in
# watcher memory. Watcher.Step's "application is current" branch
# (internal/watch/watch.go) only calls removePrepared(), which is a no-op
# when w.prepared is nil; nothing ever scans the filesystem next to AppPath
# for a sibling ".new" directory abandoned by an earlier process. See
# ephemeral/proof/bug-adjudication.md's Bug B fix, which only proved the
# in-memory case.
#
# Exit status is nonzero on the current production implementation and
# becomes zero only once a Step call through the "application is current"
# branch removes a pre-existing, unreferenced staged-bundle sibling
# directory from disk.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
exec go test -count=1 -v -run '^TestWatcherRemovesPreExistingOrphanResidueWithNoInMemoryBookkeeping$' ./internal/watch/...
