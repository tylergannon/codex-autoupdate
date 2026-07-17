#!/bin/bash
set -euo pipefail

readonly module="github.com/tylergannon/codex-autoupdate/cmd/codex-autoupdate"

die() {
  printf 'codex-autoupdate installer: %s\n' "$*" >&2
  exit 1
}

app_path_from_args() {
  local app_path="/Applications/ChatGPT.app"
  while (($#)); do
    # Keep these two forms aligned with Cobra's persistent --app-path flag.
    case "$1" in
      --app-path)
        shift
        (($#)) || die "--app-path requires a value"
        app_path=$1
        ;;
      --app-path=*)
        app_path=${1#--app-path=}
        [[ -n "$app_path" ]] || die "--app-path requires a value"
        ;;
    esac
    shift
  done
  printf '%s\n' "$app_path"
}

resolve_user_home() {
  local username record home_dir
  username=$(id -un)
  record=$(/usr/bin/dscl . -read "/Users/$username" NFSHomeDirectory) || die "could not resolve the logged-in user's home directory"
  home_dir=$(printf '%s\n' "$record" | /usr/bin/sed -E 's/^NFSHomeDirectory:[[:space:]]*//')
  [[ -n "$home_dir" && "$home_dir" = /* ]] || die "directory services returned an invalid home directory"
  printf '%s\n' "$home_dir"
}

preflight() {
  [[ "$(uname -s)" == "Darwin" ]] || die "macOS is required"
  command -v go >/dev/null 2>&1 || die "Go is required: https://go.dev/doc/install"
  case " $(id -Gn) " in
    *" admin "*) ;;
    *) die "the logged-in user must be a macOS administrator" ;;
  esac

  local app_path
  app_path=$(app_path_from_args "$@")
  [[ -d "$app_path" ]] || die "ChatGPT Desktop is not installed at $app_path"
}

install_for_home() {
  local home_dir=$1
  shift
  local install_dir="$home_dir/Library/Application Support/codex-autoupdate"
  local binary="$install_dir/codex-autoupdate"

  /bin/mkdir -p "$install_dir"
  GOBIN="$install_dir" go install "$module@latest"
  [[ -x "$binary" ]] || die "go install did not create $binary"

  "$binary" "$@" install

  printf '\nInstalled %s\n' "$("$binary" --version)"
  printf 'Binary: %s\n' "$binary"
  printf 'Status: %q status\n' "$binary"
  printf 'Logs: %s\n' "$home_dir/Library/Logs/codex-autoupdate"
  printf 'Uninstall: %q uninstall\n' "$binary"
}

main() {
  preflight "$@"
  local home_dir
  home_dir=$(resolve_user_home)
  install_for_home "$home_dir" "$@"
}

main "$@"
