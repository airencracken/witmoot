#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Check that a source archive holds exactly the tracked files of this checkout.
cd "$(dirname "$0")/../.." || exit 1
fail() { printf '%s\n' "$*" >&2; exit 1; }
[ "$#" = 1 ] && [ -f "$1" ] || fail 'Usage: check-source.sh SOURCE_ARCHIVE'
work=$(mktemp -d) || exit 1
trap 'rm -rf "$work"' EXIT
trap 'exit 130' HUP INT TERM
mkdir "$work/source" || exit 1
tar -xzf "$1" -C "$work/source" --strip-components=1 || exit 1
cmp go.mod "$work/source/go.mod" || fail 'Source archive does not match the build.'
cmp .goreleaser.yaml "$work/source/.goreleaser.yaml" || fail 'Source archive is missing current release configuration.'
test -f "$work/source/cmd/witmoot/main.go" || fail 'Source archive is incomplete.'
git ls-files > "$work/tracked" || fail 'Cannot list tracked files.'
[ -s "$work/tracked" ] || fail 'No tracked files to compare.'
while IFS= read -r source_file; do
	cmp "$source_file" "$work/source/$source_file" || fail "Source archive differs: $source_file"
done < "$work/tracked"
(cd "$work/source" && find . ! -type d) > "$work/found" || exit 1
sed 's|^\./||' "$work/found" | LC_ALL=C sort > "$work/archived" || exit 1
LC_ALL=C sort "$work/tracked" > "$work/expected" || exit 1
cmp -s "$work/expected" "$work/archived" || fail 'Source archive holds files that are not tracked.'
