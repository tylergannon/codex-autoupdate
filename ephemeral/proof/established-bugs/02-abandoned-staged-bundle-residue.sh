#!/usr/bin/env bash
# Bug B: when an application updates itself independently while
# codex-autoupdate holds a staged replacement, Watcher.Step's "application is
# current" branch (internal/watch/watch.go) abandons the on-disk staged bundle
# forever; only in-memory bookkeeping is cleared. Live evidence: an 801 MiB
# /Applications/.Claude.app.codex-autoupdate-1_34493_1.new left on the host.
# See ephemeral/proof/bug-adjudication.md.
#
# Exit status is nonzero on the current production implementation and becomes
# zero only once the abandoned staged-bundle residue is removed.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
exec go test -count=1 -v -run '^TestWatcherRemovesStagedResidueWhenApplicationBecomesCurrentIndependently$' ./internal/watch/...
