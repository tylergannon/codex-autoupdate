#!/bin/bash
set -euo pipefail

if [ "${CODEX_AUTOUPDATE_LIVE_PROOF:-}" != "restart-chatgpt" ]; then
  echo "refusing: set CODEX_AUTOUPDATE_LIVE_PROOF=restart-chatgpt to acknowledge the real restart" >&2
  exit 2
fi

binary=${1:-./codex-autoupdate}
case "$binary" in
  /*) ;;
  *) binary="$(pwd)/${binary#./}" ;;
esac

if [ ! -x "$binary" ]; then
  echo "not executable: $binary" >&2
  exit 2
fi

proof_root="$(pwd)/ephemeral/proof/$(date +%Y%m%d%H%M%S)-live-update"
mkdir -p "$proof_root"

"$binary" check --json >"$proof_root/before.json"
before_build=$(jq -r '.installed.Build' "$proof_root/before.json")
available_build=$(jq -r '.available.Build' "$proof_root/before.json")
before_server_pid=$(jq -r '.activity.AppServerPID' "$proof_root/before.json")
before_app_pid=$(/usr/bin/pgrep -f '^/Applications/ChatGPT[.]app/Contents/MacOS/ChatGPT( |$)' | /usr/bin/head -n 1 || true)
before_app_pid=${before_app_pid:-0}

if [ "$available_build" -le "$before_build" ]; then
  echo "no newer stable build is available; evidence written to $proof_root" >&2
  exit 3
fi
if [ "$before_server_pid" -eq 0 ]; then
  echo "ChatGPT Desktop app-server is not running; this would not prove a restart" >&2
  exit 3
fi

echo "proof directory: $proof_root"
echo "installed build $before_build; proving update to $available_build"
echo "during the idle wait, run the two harmless Desktop tasks described in human-proof.md"

"$binary" \
  --idle-window 1m \
  --activity-poll-interval 2s \
  run --once 2>&1 | tee "$proof_root/watcher.log"

"$binary" check --json >"$proof_root/after.json"
after_build=$(jq -r '.installed.Build' "$proof_root/after.json")
after_server_pid=$(jq -r '.activity.AppServerPID' "$proof_root/after.json")
after_app_pid=$(/usr/bin/pgrep -f '^/Applications/ChatGPT[.]app/Contents/MacOS/ChatGPT( |$)' | /usr/bin/head -n 1 || true)
after_app_pid=${after_app_pid:-0}

if [ "$after_build" -ne "$available_build" ]; then
  echo "installed build $after_build does not match expected build $available_build" >&2
  exit 4
fi
if [ "$after_server_pid" -eq 0 ]; then
  echo "Desktop app-server is not discoverable after the update" >&2
  exit 5
fi
if [ "$after_app_pid" -eq 0 ]; then
  echo "updated Desktop application is not running" >&2
  exit 5
fi
if [ "$before_app_pid" -ne 0 ] && [ "$after_app_pid" -eq "$before_app_pid" ]; then
  echo "Desktop application PID did not change: before=$before_app_pid after=$after_app_pid" >&2
  exit 5
fi

/usr/bin/codesign --verify --deep --strict --verbose=2 /Applications/ChatGPT.app 2>"$proof_root/codesign.txt"
/usr/sbin/spctl --assess --type execute --verbose=2 /Applications/ChatGPT.app 2>"$proof_root/gatekeeper.txt"

if ! grep -q "Desktop idle window satisfied" "$proof_root/watcher.log" || ! grep -q "ChatGPT Desktop update completed" "$proof_root/watcher.log"; then
  echo "watcher log is missing required lifecycle evidence" >&2
  exit 6
fi
if [ "$before_app_pid" -ne 0 ] && ! grep -q "requesting graceful ChatGPT Desktop shutdown" "$proof_root/watcher.log"; then
  echo "watcher log is missing the graceful shutdown event" >&2
  exit 6
fi

cat >"$proof_root/human-observations.txt" <<'EOF'
Fill in after inspecting the restarted app:
- First deliberate task observed and idle countdown blocked/reset:
- Second deliberate task observed and idle countdown reset:
- Existing task history visible after restart:
- Any unexpected UI or recovery behavior:
EOF

echo "automated live checks passed; record UI observations in $proof_root/human-observations.txt"
