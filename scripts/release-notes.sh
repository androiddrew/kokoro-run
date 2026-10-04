#!/bin/sh
# Print the CHANGELOG.md section for a release, which becomes its GitHub release
# notes. Fails if the section is missing or empty, so a version can't be released
# without a changelog entry.
#
#   scripts/release-notes.sh v0.2.0 [CHANGELOG.md]
set -eu
version="${1#v}"
changelog="${2:-CHANGELOG.md}"
notes="$(awk -v v="$version" '
	index($0, "## [" v "]") == 1 { found = 1; next }
	found && /^## \[/ { exit }
	/^\[[^]]+\]: / { next }  # link references at the end of the file
	found { print }
' "$changelog" | sed -e '/./,$!d' | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')"
if [ -z "$notes" ]; then
	echo "$changelog has no entry for $version: add a \"## [$version] - YYYY-MM-DD\" section" >&2
	exit 1
fi
printf '%s\n' "$notes"
