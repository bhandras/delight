#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Ensure an iOS Simulator is booted and print its UDID.

If a simulator is already booted, prints the first booted UDID.
Otherwise, boots a simulator matching the requested device name (creating one
if needed) and waits for boot completion.

Usage:
  cli/scripts/ensure_ios_sim_booted.sh [device-name]

Examples:
  cli/scripts/ensure_ios_sim_booted.sh "iPhone 16"
  cli/scripts/ensure_ios_sim_booted.sh
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

device_name="${1:-iPhone 16}"

require() {
  local bin="$1"
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "Missing required tool: $bin" >&2
    exit 1
  fi
}

require xcrun
require python3

get_simctl_json() {
  local what="$1"
  xcrun simctl list --json "$what"
}

booted_udid="$(get_simctl_json devices | python3 -c '
import json,sys
d=json.load(sys.stdin)
devices=d.get("devices") or {}
for rt,devs in devices.items():
  for dev in devs:
    if dev.get("state")=="Booted":
      udid=dev.get("udid")
      if udid:
        print(udid)
        sys.exit(0)
sys.exit(1)
' || true)"

if [[ -n "$booted_udid" ]]; then
  echo "$booted_udid"
  exit 0
fi

select_device_udid="$(python3 -c '
import json,sys,re,subprocess

device_name=sys.argv[1]

devices=json.loads(subprocess.check_output(["xcrun","simctl","list","--json","devices"]))
runtimes=json.loads(subprocess.check_output(["xcrun","simctl","list","--json","runtimes"]))

runtime_by_id={}
for rt in runtimes.get("runtimes") or []:
  runtime_by_id[rt.get("identifier")]=rt

def ios_runtime_version_tuple(rt):
  # Prefer rt["version"] if present, else parse from name ("iOS 18.6").
  ver=rt.get("version") or ""
  if ver:
    return tuple(int(p) for p in ver.split(".") if p.isdigit())
  name=rt.get("name") or ""
  m=re.search(r"iOS\\s+([0-9.]+)", name)
  if not m:
    return tuple()
  return tuple(int(p) for p in m.group(1).split(".") if p.isdigit())

best=None
for runtime_id, devs in (devices.get("devices") or {}).items():
  rt=runtime_by_id.get(runtime_id) or {}
  if not (rt.get("isAvailable") or rt.get("availability","")=="(available)"):
    continue
  if not (rt.get("name","").startswith("iOS") or "iOS" in rt.get("name","")):
    continue
  for dev in devs:
    if dev.get("name")!=device_name:
      continue
    if dev.get("isAvailable") is False:
      continue
    udid=dev.get("udid")
    if not udid:
      continue
    cand=(ios_runtime_version_tuple(rt), udid)
    if best is None or cand[0]>best[0]:
      best=cand

if best:
  print(best[1])
  sys.exit(0)
sys.exit(1)
' "$device_name" || true)"

if [[ -z "$select_device_udid" ]]; then
  echo "No existing simulator named \"$device_name\"; creating one." >&2

  devicetype_id="$(get_simctl_json devicetypes | python3 -c '
import json,sys
want=sys.argv[1]
d=json.load(sys.stdin)
for dt in d.get("devicetypes") or []:
  if dt.get("name")==want and dt.get("identifier"):
    print(dt["identifier"])
    sys.exit(0)
sys.exit(1)
' "$device_name")"

  runtime_id="$(get_simctl_json runtimes | python3 -c '
import json,sys,re
d=json.load(sys.stdin)

def parse_tuple(rt):
  ver=rt.get("version") or ""
  if ver:
    return tuple(int(p) for p in ver.split(".") if p.isdigit())
  name=rt.get("name") or ""
  m=re.search(r"iOS\\s+([0-9.]+)", name)
  if not m:
    return tuple()
  return tuple(int(p) for p in m.group(1).split(".") if p.isdigit())

cands=[]
for rt in d.get("runtimes") or []:
  if not rt.get("identifier"):
    continue
  if not (rt.get("name","").startswith("iOS") or "iOS" in rt.get("name","")):
    continue
  if not (rt.get("isAvailable") or rt.get("availability","")=="(available)"):
    continue
  cands.append((parse_tuple(rt), rt["identifier"]))

if not cands:
  sys.exit(1)
cands.sort()
print(cands[-1][1])
' )"

  new_name="Delight ${device_name}"
  if xcrun simctl list devices --json | python3 -c '
import json,sys
want=sys.argv[1]
d=json.load(sys.stdin)
for rt,devs in (d.get("devices") or {}).items():
  for dev in devs:
    if dev.get("name")==want:
      sys.exit(0)
sys.exit(1)
' "$new_name"; then
    new_name="${new_name} $(date +%s)"
  fi

  select_device_udid="$(xcrun simctl create "$new_name" "$devicetype_id" "$runtime_id")"
fi

udid="$select_device_udid"
if [[ -z "$udid" ]]; then
  echo "Failed to find or create a simulator for \"$device_name\"." >&2
  exit 1
fi

xcrun simctl boot "$udid" >/dev/null 2>&1 || true
xcrun simctl bootstatus "$udid" -b

echo "$udid"

