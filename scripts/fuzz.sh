#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# fuzz.sh runs every fuzz target in the module for a short time. CI uses it for
# the short fuzz runs of the quality pipeline (Code-ADR-0001). Findings end up
# in testdata/fuzz of the affected package and must be committed as regression
# cases together with the fix.
#
# FUZZTIME sets the duration per target (default 30s).
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fuzztime=${FUZZTIME:-30s}

# go test -list prints the matching test names of a package followed by an
# "ok <package> <duration>" line.
listing=$(go test -run='^$' -list='^Fuzz' ./...)

targets=()
pending=()
while IFS= read -r line; do
	case $line in
	Fuzz*) pending+=("$line") ;;
	ok*)
		read -r _ pkg _ <<<"$line"
		for name in "${pending[@]}"; do
			targets+=("$pkg $name")
		done
		pending=()
		;;
	esac
done <<<"$listing"

if ((${#targets[@]} == 0)); then
	echo "No fuzz targets found."
	exit 0
fi

for target in "${targets[@]}"; do
	read -r pkg name <<<"$target"
	printf '\n==> %s %s (%s)\n' "$pkg" "$name" "$fuzztime"
	go test -run='^$' -fuzz="^${name}\$" -fuzztime="$fuzztime" "$pkg"
done
