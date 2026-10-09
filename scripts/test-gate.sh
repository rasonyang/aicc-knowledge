#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# CI test gate: runs the whole suite and fails when a test failed OR when a
# test was skipped that is not listed in .ci-allowed-skips.txt, so a CI job can
# never go green by silently skipping the tests that need a live service.
#
# Usage: scripts/test-gate.sh [extra go test args]
# Env:
#   KB_GATE_ALLOWED_SKIPS  allowlist file (default .ci-allowed-skips.txt)
#   KB_GATE_MIN_PASS       fail below this many passed tests (default 400)
#   KB_GATE_JSON           keep the `go test -json` stream in this file
# Needs: go, jq.
set -euo pipefail
cd "$(dirname "$0")/.."

allow=${KB_GATE_ALLOWED_SKIPS:-.ci-allowed-skips.txt}
min_pass=${KB_GATE_MIN_PASS:-400}
json=${KB_GATE_JSON:-$(mktemp)}
trap '[ -n "${KB_GATE_JSON:-}" ] || rm -f "$json"' EXIT

# Entries are `package.TestName`, package being the last import-path element.
allowed=$(grep -vE '^[[:space:]]*(#|$)' "$allow" | sed 's/[[:space:]]*#.*$//; s/[[:space:]]*$//' || true)

# Human-readable log while the suite runs; the JSON stream is kept for the gate.
rc=0
go test -race -count=1 -json "$@" ./... | tee "$json" |
	jq -rj --unbuffered 'fromjson? | select(.Action == "output") | .Output' || rc=${PIPESTATUS[0]}

summary=$(jq -R 'fromjson? | select(.Test != null and (.Action == "pass" or .Action == "fail" or .Action == "skip"))
	| {action: .Action, name: ((.Package | split("/") | last) + "." + .Test)}' "$json" | jq -s .)
count() { jq --arg a "$1" '[.[] | select(.action == $a)] | length' <<<"$summary"; }
pass=$(count pass) fail=$(count fail) skip=$(count skip)
# Package-level failures (build errors, panics, TestMain exits) carry no Test.
badpkgs=$(jq -R 'fromjson? | select(.Test == null and .Package != null and .Action == "fail") | .Package' -r "$json")

echo
echo "test gate: pass=$pass fail=$fail skip=$skip"

status=0
if [ "$rc" -ne 0 ] || [ "$fail" -ne 0 ] || [ -n "$badpkgs" ]; then
	echo "test gate: FAILED: go test exited $rc"
	jq -r '.[] | select(.action == "fail") | "  failed test: " + .name' <<<"$summary"
	[ -z "$badpkgs" ] || sed 's/^/  failed package: /' <<<"$badpkgs"
	status=1
fi

unlisted=""
while IFS= read -r name; do
	[ -n "$name" ] || continue
	ok=0
	while IFS= read -r a; do
		[ -n "$a" ] || continue
		# An entry also covers the subtests of that test.
		if [ "$name" = "$a" ] || [[ "$name" == "$a"/* ]]; then ok=1; break; fi
	done <<<"$allowed"
	[ "$ok" -eq 1 ] || unlisted+="  $name"$'\n'
done < <(jq -r '.[] | select(.action == "skip") | .name' <<<"$summary")
if [ -n "$unlisted" ]; then
	echo "test gate: FAILED: skipped tests not in $allow (a service or env var the job should provide is missing):"
	printf '%s' "$unlisted"
	status=1
fi

if [ "$pass" -lt "$min_pass" ]; then
	echo "test gate: FAILED: only $pass tests passed, expected at least $min_pass (KB_GATE_MIN_PASS)"
	status=1
fi

[ "$status" -eq 0 ] && echo "test gate: OK"
exit "$status"
