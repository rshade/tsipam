#!/usr/bin/env bash
# Writes the NOTICE shipped in the release archives: the committed NOTICE header
# followed by the license and NOTICE text of every non-standard module linked
# into a release target. goreleaser runs it as a before hook, so the inventory
# always matches the go.mod being released.
#
# Usage: scripts/notice.sh OUTPUT
set -euo pipefail

out=${1:?usage: scripts/notice.sh OUTPUT}
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# A module with no license file of its own needs <module path>.txt here, stating
# where its license is found. license-check reads the same directory.
exceptions=$root/scripts/license-exceptions

targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)
# The build tags in .goreleaser.yaml, so the inventory is what an archive links.
tags=ax_no_grpc,ax_no_otlp
sep='==============================================================================='

modules() {
  local target
  for target in "${targets[@]}"; do
    GOOS=${target%/*} GOARCH=${target#*/} go -C "$root" list -deps -tags "$tags" \
      -f '{{with .Module}}{{if not .Main}}{{with .Replace}}{{.Path}} {{.Version}} {{.Dir}}{{else}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}{{end}}' \
      ./cmd/tsipam || exit 1
  done | sort -u
}

# Lists the regular files directly in DIR whose names match the find tests that
# follow, one per line, in byte order.
files_in() {
  local dir=$1
  shift
  find "$dir" -maxdepth 1 -type f \( "$@" \) | LC_ALL=C sort
}

# Prints a file verbatim, adding a final newline if it lacks one, then a blank
# line.
emit() {
  cat "$1"
  [ -z "$(tail -c 1 "$1")" ] || echo
  echo
}

# Emits each file in the newline-separated LIST under a "LABEL (from NAME):"
# heading.
emit_all() {
  local label=$1 list=$2 file
  while IFS= read -r file; do
    [ -n "$file" ] || continue
    printf '%s (from %s):\n\n' "$label" "${file##*/}"
    emit "$file"
  done <<<"$list"
}

# Read the inventory up front: a failing go list inside a process substitution
# would not stop the script, and the NOTICE would silently lose modules.
inventory=$(modules)

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

{
  emit "$root/NOTICE"
  cat <<EOF
Third-party dependency notices

This distribution includes compiled code from the third-party modules below.
The inventory is the union of non-standard modules linked into the linux,
darwin, and windows amd64/arm64 tsipam release targets, as resolved by go.mod
and go.sum. License and NOTICE text is reproduced verbatim from each pinned
module.

EOF
  while read -r path version dir; do
    printf '%s\n%s %s\n%s\n' "$sep" "$path" "$version" "$sep"
    licenses=$(files_in "$dir" -iname 'LICENSE' -o -iname 'LICENSE[.-]*' -o -iname 'LICENCE*' -o -iname 'COPYING*')
    notices=$(files_in "$dir" -iname 'NOTICE*')
    if [ -z "$licenses" ]; then
      if [ ! -f "$exceptions/$path.txt" ]; then
        echo "notice.sh: $path $version has no license file; add $exceptions/$path.txt after checking its terms" >&2
        exit 1
      fi
      cat "$exceptions/$path.txt"
      while IFS= read -r file; do
        [ -n "$file" ] || continue
        printf 'Upstream NOTICE text:\n\n'
        emit "$file"
      done <<<"$notices"
      continue
    fi
    emit_all "License text" "$licenses"
    emit_all "NOTICE text" "$notices"
  done <<<"$inventory"
} > "$tmp"

mkdir -p "$(dirname "$out")"
# Strip the final blank line so the file ends with exactly one newline.
sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$tmp" > "$out"
