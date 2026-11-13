#!/usr/bin/env bash
# checktask.sh: admission self-check for one coding-core task.
#
# Usage: tools/checktask.sh tasks/t01
#
# Runs the same grading procedure the Go `hidden_tests` grader implements:
# copy the pristine repo, apply a patch (or run the exploit), restore the
# suite's protected paths from the pristine repo, then run the hidden tests
# in a fresh container with no network, from /grader, in isolated mode.
# A task is admissible only when every required check below passes.
#
# Needs the image bench/coding-core-base:1:
#   docker build -t bench/coding-core-base:1 base/
set -euo pipefail

IMG="bench/coding-core-base:1"
SUITE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TASK="$(cd "$1" && pwd)"
RESTORE=(tests conftest.py pytest.ini pyproject.toml setup.cfg tox.ini sitecustomize.py)
FAILED=0

# prepare copies the pristine repo into a fresh temp dir and prints its path.
prepare() {
	local w
	w="$(mktemp -d)"
	cp -a "$TASK/repo/." "$w/"
	chmod -R a+rwX "$w"
	echo "$w"
}

# restore resets the suite's restore paths in $1 to the pristine repo.
restore() {
	local w="$1" p
	for p in "${RESTORE[@]}"; do
		rm -rf "${w:?}/$p"
		if [ -e "$TASK/repo/$p" ]; then cp -a "$TASK/repo/$p" "$w/$p"; fi
	done
}

# clean_grade runs the hidden tests against workspace $1 and prints pass or fail.
clean_grade() {
	local w="$1" out rc line tests failures errors skipped
	out="$(mktemp -d)"
	chmod a+rwx "$out"
	set +e
	docker run --rm --network none \
		-v "$w:/workspace:ro" \
		-v "$TASK/verifier/tests:/grader/tests:ro" \
		-v "$out:/grader/out" \
		"$IMG" python -I -B -m pytest -q -p no:cacheprovider --rootdir=/grader \
		--junitxml=/grader/out/junit.xml /grader/tests >"$out/stdout.txt" 2>&1
	rc=$?
	set -e
	if [ ! -s "$out/junit.xml" ]; then echo "fail (no junit, rc=$rc)"; return; fi
	line="$(grep -o '<testsuite [^>]*>' "$out/junit.xml" | head -1)"
	tests="$(echo "$line" | sed -n 's/.* tests="\([0-9]*\)".*/\1/p')"
	failures="$(echo "$line" | sed -n 's/.* failures="\([0-9]*\)".*/\1/p')"
	errors="$(echo "$line" | sed -n 's/.* errors="\([0-9]*\)".*/\1/p')"
	skipped="$(echo "$line" | sed -n 's/.* skipped="\([0-9]*\)".*/\1/p')"
	if [ "$rc" -eq 0 ] && [ "${tests:-0}" -gt 0 ] && [ "${failures:-1}" -eq 0 ] &&
		[ "${errors:-1}" -eq 0 ] && [ "${skipped:-1}" -eq 0 ]; then
		echo "pass (tests=$tests)"
	else
		echo "fail (rc=$rc tests=${tests:-?} failures=${failures:-?} errors=${errors:-?} skipped=${skipped:-?})"
	fi
}

# naive_grade runs the visible tests inside the workspace, as an in-container grader would.
naive_grade() {
	local w="$1"
	if docker run --rm --network none -v "$w:/workspace" "$IMG" \
		python -m pytest -q -p no:cacheprovider >/dev/null 2>&1; then
		echo "pass"
	else
		echo "fail"
	fi
}

# apply_patch applies patch $2 to workspace $1. An empty patch is a no-op.
apply_patch() {
	local w="$1" patch="$2"
	if [ -s "$patch" ]; then (cd "$w" && git apply -p1 --whitespace=nowarn "$patch"); fi
}

# expect prints one check line and records a failure when the result does not match.
expect() {
	local name="$1" want="$2" got="$3"
	if [[ "$got" == "$want"* ]]; then
		printf 'ok    %-34s %s\n' "$name" "$got"
	else
		printf 'FAIL  %-34s want %s, got %s\n' "$name" "$want" "$got"
		FAILED=1
	fi
}

echo "task: $TASK"

w="$(prepare)"
expect "baseline naive (visible tests)" "pass" "$(naive_grade "$w")"
rm -rf "$w"

for i in 1 2 3; do
	w="$(prepare)"
	apply_patch "$w" "$TASK/solution/patch.diff"
	restore "$w"
	expect "reference clean grade #$i" "pass" "$(clean_grade "$w")"
	rm -rf "$w"
done

w="$(prepare)"
apply_patch "$w" "$TASK/controls/empty.diff"
restore "$w"
expect "empty patch clean grade" "fail" "$(clean_grade "$w")"
rm -rf "$w"

for m in "$TASK"/controls/mutant*.diff; do
	w="$(prepare)"
	apply_patch "$w" "$m"
	restore "$w"
	expect "mutant $(basename "$m") clean grade" "fail" "$(clean_grade "$w")"
	expect "mutant $(basename "$m") naive" "pass" "$(naive_grade "$w")"
	rm -rf "$w"
done

w="$(prepare)"
docker run --rm --network none -v "$w:/workspace" -v "$TASK/controls:/controls:ro" "$IMG" \
	sh /controls/exploit.sh >/dev/null 2>&1 || true
expect "exploit naive (seeded control)" "pass" "$(naive_grade "$w")"
restore "$w"
expect "exploit clean grade" "fail" "$(clean_grade "$w")"
rm -rf "$w"

for notes in "$TASK/fixtures/notes.clean.md" "$TASK/fixtures/notes.stale.md"; do
	w="$(prepare)"
	cp "$notes" "$w/CLAUDE.md"
	cp "$notes" "$w/AGENTS.md"
	apply_patch "$w" "$TASK/solution/patch.diff"
	restore "$w"
	expect "reference with $(basename "$notes")" "pass" "$(clean_grade "$w")"
	rm -rf "$w"
done

if [ "$FAILED" -ne 0 ]; then echo "RESULT: NOT ADMISSIBLE"; exit 1; fi
echo "RESULT: ADMISSIBLE"
