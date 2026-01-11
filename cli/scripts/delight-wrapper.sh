#!/usr/bin/env bash
set -euo pipefail

# delight-wrapper.sh runs a command in a restart loop. If the command exits with
# DELIGHT_RESTART_CODE (default: 75), the wrapper re-launches it in the same
# directory with the same arguments.
#
# Example:
#   ./cli/scripts/delight-wrapper.sh delight run
#
# When paired with the mobile "Restart CLI" action, the CLI exits with the
# restart code so this wrapper will automatically re-launch it.

RESTART_CODE="${DELIGHT_RESTART_CODE:-75}"
MAX_RESTARTS="${DELIGHT_MAX_RESTARTS:-20}"

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <command> [args...]" >&2
  exit 2
fi

orig_pwd="$(pwd -P)"
restarts=0

while true; do
  (
    cd "$orig_pwd"
    "$@"
  )
  code=$?

  if [[ $code -eq $RESTART_CODE ]]; then
    restarts=$((restarts + 1))
    if [[ $restarts -gt $MAX_RESTARTS ]]; then
      echo "$0: too many restarts ($restarts), aborting" >&2
      exit 1
    fi
    continue
  fi

  exit $code
done
