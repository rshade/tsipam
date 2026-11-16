#!/usr/bin/env bash
# Writes the NOTICE shipped in the release archives: the committed NOTICE header
# followed by the license and NOTICE text of every non-standard module linked
# into a release target. goreleaser runs it as a before hook, so the inventory
# always matches the go.mod being released.
#
# Usage: scripts/notice.sh OUTPUT
set -euo pipefail

out=${1:?usage: scripts/notice.sh OUTPUT}
case $out in
  /*) ;;
  *) out=$PWD/$out ;;
esac
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64)
sep='==============================================================================='

modules() {
  local target
  for target in "${targets[@]}"; do
    GOOS=${target%/*} GOARCH=${target#*/} go list -deps \
      -f '{{with .Module}}{{if not .Main}}{{with .Replace}}{{.Path}} {{.Version}} {{.Dir}}{{else}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}{{end}}' \
      ./cmd/tsipam
  done | sort -u
}

# Prints a file verbatim, adding a final newline if it lacks one, then a blank
# line.
emit() {
  cat "$1"
  [ -z "$(tail -c 1 "$1")" ] || echo
  echo
}

# Modules whose archive has no license file. Each entry names where the license
# is stated; review it whenever the module's version changes.
no_license_file() {
  case $1 in
    github.com/jaxxstorm/vers)
      cat <<'EOF'
No standalone license file is present in this module archive. Its
published README identifies the project as Apache-2.0; the Apache-2.0
terms are provided in LICENSE. The upstream NOTICE follows.

EOF
      ;;
    *) return 1 ;;
  esac
}

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

{
  emit NOTICE
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
    licenses=$(find "$dir" -maxdepth 1 -type f \
      \( -iname 'LICENSE' -o -iname 'LICENSE[.-]*' -o -iname 'LICENCE*' -o -iname 'COPYING*' \) | LC_ALL=C sort)
    notices=$(find "$dir" -maxdepth 1 -type f -iname 'NOTICE*' | LC_ALL=C sort)
    if [ -z "$licenses" ]; then
      if ! no_license_file "$path"; then
        echo "notice.sh: $path $version has no license file; add it to no_license_file after checking its terms" >&2
        exit 1
      fi
      for file in $notices; do
        printf 'Upstream NOTICE text:\n\n'
        emit "$file"
      done
      continue
    fi
    for file in $licenses; do
      printf 'License text (from %s):\n\n' "${file##*/}"
      emit "$file"
    done
    for file in $notices; do
      printf 'NOTICE text (from %s):\n\n' "${file##*/}"
      emit "$file"
    done
  done < <(modules)
} > "$tmp"

mkdir -p "$(dirname "$out")"
# Strip the final blank line so the file ends with exactly one newline.
sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$tmp" > "$out"
