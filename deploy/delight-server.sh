#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Delight public server helper (Docker Compose + Caddy HTTPS)

Usage:
  deploy/delight-server.sh init [--data-dir DIR]
  deploy/delight-server.sh up   [--data-dir DIR]
  deploy/delight-server.sh down [--data-dir DIR]
  deploy/delight-server.sh logs [--data-dir DIR] [--service NAME]
  deploy/delight-server.sh ps   [--data-dir DIR]

Environment (.env file under the data dir):
  DELIGHT_HOSTNAME       Public hostname (required), e.g. api.example.com
  DELIGHT_MASTER_SECRET  Server master secret (required)
  DELIGHT_EMAIL          Optional email for ACME registration

Defaults:
  --data-dir defaults to ./deploy-data (relative to repo root)

Examples:
  deploy/delight-server.sh init
  deploy/delight-server.sh up
  deploy/delight-server.sh logs --service caddy
EOF
}

cmd=""
data_dir="./deploy-data"
service=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    init|up|down|logs|ps)
      cmd="$1"
      shift
      ;;
    --data-dir)
      data_dir="$2"
      shift 2
      ;;
    --service)
      service="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "$cmd" ]]; then
  usage
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
cd "$repo_root"

compose_file="deploy/docker-compose.yml"

if [[ "$data_dir" != /* ]]; then
  data_dir="${repo_root}/${data_dir}"
fi

env_file="${data_dir}/.env"

mkdir -p "${data_dir}/server" "${data_dir}/caddy/data" "${data_dir}/caddy/config"

if [[ "$cmd" == "init" ]]; then
  if [[ -f "$env_file" ]]; then
    echo "Env file already exists: ${env_file}" >&2
    exit 0
  fi

  cat >"$env_file" <<'EOF'
# Public hostname for Caddy (required).
DELIGHT_HOSTNAME=api.example.com

# Master secret used for JWT signing (required).
DELIGHT_MASTER_SECRET=change-me

# Optional email for ACME account registration.
DELIGHT_EMAIL=
EOF

  echo "Wrote ${env_file}"
  echo "Edit it, then run: ./deploy/delight-server.sh up"
  exit 0
fi

if [[ ! -f "$env_file" ]]; then
  echo "Missing env file: ${env_file}" >&2
  echo "Run: ./deploy/delight-server.sh init --data-dir ${data_dir}" >&2
  exit 2
fi

export DELIGHT_DATA_DIR="$data_dir"

case "$cmd" in
  up)
    docker compose --env-file "$env_file" -f "$compose_file" up -d --build
    ;;
  down)
    docker compose --env-file "$env_file" -f "$compose_file" down
    ;;
  ps)
    docker compose --env-file "$env_file" -f "$compose_file" ps
    ;;
  logs)
    if [[ -n "$service" ]]; then
      docker compose --env-file "$env_file" -f "$compose_file" logs -f "$service"
    else
      docker compose --env-file "$env_file" -f "$compose_file" logs -f
    fi
    ;;
  *)
    echo "Unknown command: $cmd" >&2
    usage
    exit 2
    ;;
esac

