#!/usr/bin/env bash
# Bug D: Watcher.Step (internal/watch/watch.go) unconditionally logs the same
# status record ("waiting for active work to finish" / "application is
# current") on every poll tick, even when the reported state has not changed
# since the previous tick. Live evidence: the installed watcher's stderr.log
# had grown to 35,470 lines / 5,119,873 bytes after ~4 days, dominated by
# repeated identical records at ~5s cadence during pending periods. See
# ephemeral/proof/bug-adjudication.md.
#
# Exit status is nonzero on the current production implementation and becomes
# zero only once identical consecutive status records stop being re-logged on
# every tick.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
exec go test -count=1 -v -run '^TestWatcherDoesNotLogDuplicateStatusOnUnchangedPendingState$' ./internal/watch/...
