#!/usr/bin/env bash
# Bug C: Detector.cache (internal/activity/detector.go) retains one entry per
# historical rollout file for the entire lifetime of the Codex Desktop
# app-server PID; no code path evicts an entry when its rollout file is
# archived, deleted, or otherwise gone. See ephemeral/proof/bug-adjudication.md.
#
# Exit status is nonzero on the current production implementation and becomes
# zero only once stale cache entries stop accumulating for deleted rollouts.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
exec go test -count=1 -v -run '^TestDetectorPrunesCacheEntriesForRolloutsThatNoLongerExist$' ./internal/activity/...
