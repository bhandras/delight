#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Fix commit messages that contain literal "\n" sequences (two characters: backslash + n)
by rewriting history to use real newlines.

This rewrites history for the current branch only by default.

Usage:
  scripts/fix_commit_message_newlines.sh
  scripts/fix_commit_message_newlines.sh --ref <git-ref>

Examples:
  scripts/fix_commit_message_newlines.sh
  scripts/fix_commit_message_newlines.sh --ref main

Notes:
  - Rewriting history changes commit SHAs and invalidates GPG signatures.
  - A backup branch is created before rewriting.
EOF
}

ref=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    --ref)
      ref="${2:-}"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

has_literal_backslash_n() {
  local target_ref="$1"
  git log "$target_ref" --format='%B%n==END==' | python3 -c 'import sys; data=sys.stdin.buffer.read(); sys.exit(0 if b"\\n" in data else 1)'
}

if [[ -n "$(git status --porcelain)" ]]; then
  echo "Working tree is not clean; commit or stash changes first." >&2
  exit 1
fi

if ! command -v git-filter-repo >/dev/null 2>&1 && ! command -v git >/dev/null 2>&1; then
  echo "git is required." >&2
  exit 1
fi

if ! command -v git-filter-repo >/dev/null 2>&1 && ! git filter-repo --help >/dev/null 2>&1; then
  echo "git-filter-repo is required (Homebrew: brew install git-filter-repo)." >&2
  exit 1
fi

if [[ -z "$ref" ]]; then
  ref="$(git symbolic-ref --quiet --short HEAD || true)"
  if [[ -z "$ref" ]]; then
    echo "Detached HEAD; pass an explicit --ref <name>." >&2
    exit 1
  fi
fi

if ! git rev-parse --verify "$ref" >/dev/null 2>&1; then
  echo "Invalid ref: $ref" >&2
  exit 1
fi

if ! has_literal_backslash_n "$ref"; then
  echo "No commit messages with literal \"\\n\" found on $ref; nothing to do."
  exit 0
fi

timestamp="$(date +%Y%m%d-%H%M%S)"
backup_branch="backup/fix-commit-message-newlines-${ref//\//-}-${timestamp}"

echo "Creating backup branch: $backup_branch"
git branch "$backup_branch" "$ref"

old_head="$(git rev-parse "$ref")"
echo "Rewriting commit messages on $ref (old HEAD: $old_head)"

git filter-repo \
  --force \
  --refs "$ref" \
  --message-callback 'return message.replace(b"\\n", b"\n")'

new_head="$(git rev-parse "$ref")"
echo "Rewrite complete (new HEAD: $new_head)"
echo "Backup branch preserved at: $backup_branch"

if has_literal_backslash_n "$ref"; then
  echo "Warning: some literal \"\\n\" sequences still remain on $ref." >&2
  echo "This can be expected if commit messages intentionally include \"\\n\"." >&2
else
  echo "Verified: no remaining literal \"\\n\" sequences on $ref."
fi

cat <<EOF

Next steps:
  - If you have NOT pushed these commits: you're done.
  - If you DID push them anywhere, you'll need to force-push:
      git push --force-with-lease origin $ref
EOF
