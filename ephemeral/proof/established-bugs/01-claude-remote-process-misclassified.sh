#!/usr/bin/env bash
# Bug A: a live ~/.claude/remote/ccd-cli/<version> process (Claude Code remote
# runtime, reproduced live with mcp__computer-use granted) is misclassified as
# idle by internal/activity/claude.go. See ephemeral/proof/bug-adjudication.md.
#
# Exit status is nonzero on the current production implementation and becomes
# zero only once ClaudeDetector recognizes this runtime as active.
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
exec go test -count=1 -v -run '^TestClaudeDetectorRecognizesRemoteCliRuntime$' ./internal/activity/...
