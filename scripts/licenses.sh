#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Write THIRD_PARTY_LICENSES for the dependencies compiled into
# ./cmd/aicc-knowledge (test-only and tool dependencies are not included).
#
#   scripts/licenses.sh [output-file]      default: THIRD_PARTY_LICENSES
#
# It runs google/go-licenses, fails on any license outside the allowlist
# (an unrecognised license counts as not allowed), and emits a sorted, stable
# file. Run it from `make licenses`; `make licenses-check` compares the result
# with the committed file.
set -euo pipefail
export LC_ALL=C

cd "$(dirname "$0")/.."

GO_LICENSES=${GO_LICENSES:-github.com/google/go-licenses/v2@v2.0.1}
ALLOWED=${ALLOWED:-Apache-2.0 MIT BSD-2-Clause BSD-3-Clause ISC MPL-2.0}
OUT=${1:-THIRD_PARTY_LICENSES}

self=$(go list -m)
cache=$(go env GOMODCACHE)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cat > "$work/tmpl" <<'TMPL'
{{range .}}{{.Name}}|{{.LicenseName}}|{{.LicensePath}}
{{end}}
TMPL

# The dependency graph depends on the target platform (prometheus/procfs, for
# one, is compiled in on linux only), so the report is always taken for the
# platform the image ships, whatever host runs it. The tool itself is built
# for the host; only the analysis is cross-platform.
TARGET_GOOS=${LICENSES_GOOS:-linux}
TARGET_GOARCH=${LICENSES_GOARCH:-amd64}
GOBIN="$work/bin" GOOS= GOARCH= go install "$GO_LICENSES"

if ! GOOS=$TARGET_GOOS GOARCH=$TARGET_GOARCH "$work/bin/go-licenses" report ./cmd/aicc-knowledge \
	--template "$work/tmpl" > "$work/report" 2> "$work/err"; then
	cat "$work/err" >&2
	echo "go-licenses failed" >&2
	exit 1
fi

# Our own module is not a third party.
grep -v -e "^$self|" -e "^$self/" "$work/report" | grep . > "$work/deps" || true
[ -s "$work/deps" ] || { echo "go-licenses reported no dependencies" >&2; exit 1; }

# Fail loudly on anything outside the allowlist.
bad=0
while IFS='|' read -r name spdx path; do
	ok=0
	for a in $ALLOWED; do [ "$spdx" = "$a" ] && ok=1; done
	if [ "$ok" = 0 ]; then
		echo "license not allowed: $name: '$spdx' ($path)" >&2
		bad=1
	fi
done < "$work/deps"
[ "$bad" = 0 ] || { echo "allowed: $ALLOWED" >&2; exit 1; }

# One record per license file: module, version, file in module, SPDX ids (joined), path.
awk -F'|' -v cache="$cache/" '
{
	p = $3
	if (index(p, cache) != 1) { print "license file outside module cache: " p > "/dev/stderr"; exit 1 }
	rel = substr(p, length(cache) + 1)
	at = index(rel, "@")
	mod = substr(rel, 1, at - 1)
	rest = substr(rel, at + 1)
	slash = index(rest, "/")
	ver = substr(rest, 1, slash - 1)
	file = substr(rest, slash + 1)
	while (match(mod, /![a-z]/)) mod = substr(mod, 1, RSTART - 1) toupper(substr(mod, RSTART + 1, 1)) substr(mod, RSTART + 2)
	if (!(p in ids)) ids[p] = $2; else if (index(" " ids[p] " ", " " $2 " ") == 0) ids[p] = ids[p] " " $2
	info[p] = mod "\t" ver "\t" file
}
END {
	for (p in info) {
		n = split(ids[p], a, " ")
		for (i = 1; i <= n; i++) for (j = i + 1; j <= n; j++) if (a[j] < a[i]) { t = a[i]; a[i] = a[j]; a[j] = t }
		s = a[1]; for (i = 2; i <= n; i++) s = s " AND " a[i]
		print info[p] "\t" s "\t" p
	}
}' "$work/deps" | sort -t$'\t' -k1,1 -k3,3 > "$work/records"

{
	cat <<HEADER
THIRD-PARTY LICENSES

aicc-knowledge is licensed under the Apache License, Version 2.0 (see LICENSE).
This file reproduces the licenses of the third-party Go modules compiled into
the aicc-knowledge binary (the dependency graph of ./cmd/aicc-knowledge; test
and tool dependencies are not shipped and are not listed).

Each entry gives the module, its version, the SPDX identifier go-licenses
detected, the license file inside the module, the full license text and, where
the module ships one, its NOTICE text.

This file is generated. Do not edit it. To regenerate it:

    make licenses

Generated with $GO_LICENSES. Allowed licenses: $(echo $ALLOWED | sed 's/ /, /g').
HEADER
	while IFS=$'\t' read -r mod ver file spdx path; do
		echo
		printf '%s\n' "================================================================================"
		echo "Module:  $mod"
		echo "Version: $ver"
		echo "License: $spdx"
		echo "File:    $file"
		printf '%s\n' "================================================================================"
		echo
		cat "$path"
		[ -z "$(tail -c1 "$path")" ] || echo
		# A module's NOTICE follows its top-level license file.
		moddir=$(dirname "$path")
		for n in NOTICE NOTICE.txt NOTICE.md; do
			if [ "$file" = "$(basename "$path")" ] && [ -f "$moddir/$n" ]; then
				echo
				echo "--- $n of $mod $ver ---"
				echo
				cat "$moddir/$n"
				[ -z "$(tail -c1 "$moddir/$n")" ] || echo
			fi
		done
	done < "$work/records"
} > "$work/out"

mv "$work/out" "$OUT"
echo "wrote $OUT: $(wc -l < "$work/records" | tr -d ' ') license files"
