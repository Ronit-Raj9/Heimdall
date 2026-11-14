#!/bin/sh
# Verify acme-orders tasks by hand, the way admission (`task admit`) grades
# them, with plain docker and no bench binary:
#
#   tools/verify-by-hand.sh [TASK ...]        # default: every task in suite.yaml
#
# For each task: the reference solution 3 times, the empty patch, every
# mutant, the exploit script, and the reference with the stale notes present
# as AGENTS.md and CLAUDE.md. Every check uses fresh containers with
# --network none:
#   1. prepare (root, trusted): copy repo/ into a new volume, git apply the
#      patch, reset the suite's restore paths from repo/;
#   2. verify (uid 1000, read-only root, workspace read-only): run the suite's
#      verifier command, which writes JUnit XML to /grader/out/junit.xml;
#   3. judge with tools/junit-summary.mjs: at least one test, no failures,
#      errors or skips, every required id of task.yaml ran and passed.
# The exploit runs as uid 1000 in a workspace with a git baseline; the diff
# it leaves is exported (git add -A, git diff --cached) and graded the same
# way. Everything it creates carries the label heimdall-example=1 and is
# removed on exit.
#
# Needs: docker, the suite base image (IMAGE, default
# heimdall-example/acme-orders-base:1; build it with
#   docker build -t heimdall-example/acme-orders-base:1 benchmarks/acme-orders/base).
# KEEP_WORK=1 keeps the verifier logs, JUnit reports and exploit diffs.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
PROJECT=$(dirname "$HERE")
SUITE="$PROJECT/benchmarks/acme-orders"
IMAGE=${IMAGE:-heimdall-example/acme-orders-base:1}
LABEL=heimdall-example=1
RUN_ID=hv$$
RESTORE="test package.json node.config.json" # suite.yaml verifier.restore
TIMEOUT_S=300                                # suite.yaml verifier.timeout_s

WORK=$(mktemp -d "${TMPDIR:-/tmp}/acme-verify.XXXXXX")
cleanup() {
	docker ps -aq --filter "label=heimdall-example.run=$RUN_ID" | xargs -r docker rm -f >/dev/null 2>&1 || true
	docker volume ls -q --filter "label=heimdall-example.run=$RUN_ID" | xargs -r docker volume rm -f >/dev/null 2>&1 || true
	if [ -n "${KEEP_WORK:-}" ]; then echo "kept logs, reports and the exploit diff in $WORK" >&2; else rm -rf "$WORK"; fi
}
trap cleanup EXIT INT TERM

docker image inspect "$IMAGE" >/dev/null 2>&1 || { echo "image $IMAGE not found; build benchmarks/acme-orders/base first" >&2; exit 2; }

# Same steps as the hidden_tests grader's prepare script. BENCH_NOTES adds the
# notes file under both names an agent may read, as the stale-notes probe does.
PREPARE='set -eu
cp -a /bench-src/. /workspace/
cd /workspace
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp GIT_CEILING_DIRECTORIES=/
if [ -n "${BENCH_PATCH:-}" ] && [ -s "$BENCH_PATCH" ]; then
	git -c core.hooksPath=/dev/null apply -p1 --whitespace=nowarn "$BENCH_PATCH" || exit 42
fi
if [ -n "${BENCH_NOTES:-}" ]; then
	cp "$BENCH_NOTES" /workspace/AGENTS.md
	cp "$BENCH_NOTES" /workspace/CLAUDE.md
fi
for p in "$@"; do
	rm -rf "/workspace/$p"
	if [ -e "/bench-src/$p" ] || [ -L "/bench-src/$p" ]; then
		mkdir -p "$(dirname "/workspace/$p")"
		cp -a "/bench-src/$p" "/workspace/$p"
	fi
done
chown -R 1000:1000 /workspace /grader/out'

# Same steps as the docker_workspace environment's populate script.
POPULATE='set -eu
# A new volume inherits the /workspace owner of the image (uid 1000); git as root
# refuses a repository it does not own, so hand it to uid 1000 only after the
# baseline commit.
chown 0:0 /workspace
cp -dR --preserve=mode,timestamps /bench-src/. /workspace/
cd /workspace
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp
git -c init.defaultBranch=main init -q
git -c core.hooksPath=/dev/null add -A --force
git -c core.hooksPath=/dev/null -c user.name=bench -c user.email=bench@local -c commit.gpgsign=false commit -q --no-verify -m baseline
chown -R 1000:1000 /workspace'

# Same steps as the environment's export script, writing the diff to /out.
EXPORT='set -eu
mkdir /tmp/w
cp -dR --preserve=mode,timestamps /workspace/. /tmp/w/
cd /tmp/w
rm -rf .git/index .git/index.lock .git/info .git/hooks
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp GIT_TERMINAL_PROMPT=0
g() { git -c core.hooksPath=/dev/null -c diff.external= -c core.fsmonitor=false -c core.autocrlf=false -c core.quotepath=true "$@"; }
g add -A --force
g diff --cached --binary --no-ext-diff --no-textconv --no-color --no-renames --src-prefix=a/ --dst-prefix=b/ HEAD > /out/exploit.diff'

new_volume() {
	v="acme-verify-$RUN_ID-$1"
	docker volume create --label "$LABEL" --label "heimdall-example.run=$RUN_ID" "$v" >/dev/null
	echo "$v"
}

# required_ids TASK_DIR: the verifier.required list of task.yaml, one per line.
required_ids() {
	awk '/^  required:/ {on=1; next} on && /^    - / {sub(/^    - /, ""); gsub(/"/, ""); print; next} on && !/^ *#/ {on=0}' "$1/task.yaml"
}

# grade TASK_DIR NAME PATCH [NOTES]: prints the junit summary line; returns 0 on pass.
grade() {
	task_dir=$1; name=$2; patch=$3; notes=${4:-}
	ws=$(new_volume "$name-ws")
	out="$WORK/$name-out"; mkdir -p "$out"
	set -- docker run --rm --label "$LABEL" --label "heimdall-example.run=$RUN_ID" \
		--network none --user 0 --cap-drop ALL \
		--cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add DAC_READ_SEARCH --cap-add FOWNER \
		--security-opt no-new-privileges --pids-limit 256 --memory 2g \
		-v "$ws:/workspace" -v "$out:/grader/out" -v "$task_dir/repo:/bench-src:ro" \
		-v "$patch:/bench-patch/workspace.diff:ro" -e BENCH_PATCH=/bench-patch/workspace.diff
	if [ -n "$notes" ]; then set -- "$@" -v "$notes:/bench-notes.md:ro" -e BENCH_NOTES=/bench-notes.md; fi
	set +e
	"$@" --entrypoint /bin/sh "$IMAGE" -c "$PREPARE" bench-prep $RESTORE
	code=$?
	set -e
	if [ "$code" != 0 ]; then
		docker volume rm -f "$ws" >/dev/null
		if [ "$code" = 42 ]; then echo '{"pass":false,"reason":"patch_not_applied"}'; else echo "{\"pass\":false,\"reason\":\"prepare_exit_$code\"}"; fi
		return 1
	fi
	set +e
	timeout "$TIMEOUT_S" docker run --rm --label "$LABEL" --label "heimdall-example.run=$RUN_ID" \
		--network none --user 1000 --cap-drop ALL --security-opt no-new-privileges \
		--pids-limit 512 --memory 2g --memory-swap 2g --cpus 2 \
		--read-only --tmpfs /tmp:exec,size=512m --tmpfs /home/agent:exec,size=64m,mode=0700,uid=1000,gid=1000 \
		-e HOME=/home/agent \
		-v "$ws:/workspace:ro" -v "$task_dir/verifier/tests:/grader/tests:ro" -v "$out:/grader/out" \
		--entrypoint node "$IMAGE" \
		--test --test-reporter=junit --test-reporter-destination=/grader/out/junit.xml '/grader/tests/**/*.test.mjs' \
		>"$out/verifier.log" 2>&1
	set -e
	docker volume rm -f "$ws" >/dev/null
	# shellcheck disable=SC2046
	docker run --rm --network none --label "$LABEL" --label "heimdall-example.run=$RUN_ID" \
		-v "$out:/out:ro" -v "$HERE:/tools:ro" --entrypoint node "$IMAGE" \
		/tools/junit-summary.mjs /out/junit.xml $(required_ids "$task_dir")
}

# exploit_diff TASK_DIR: runs the exploit, writes $WORK/<task>-exploit/exploit.diff.
exploit_diff() {
	task_dir=$1; task=$(basename "$task_dir")
	ws=$(new_volume "$task-exploit-ws")
	out="$WORK/$task-exploit"; mkdir -p "$out"
	run="docker run --rm --label $LABEL --label heimdall-example.run=$RUN_ID --network none --security-opt no-new-privileges"
	$run --user 0 --cap-drop ALL --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER \
		-v "$ws:/workspace" -v "$task_dir/repo:/bench-src:ro" --entrypoint /bin/sh "$IMAGE" -c "$POPULATE" >/dev/null
	set +e
	$run --user 1000 --cap-drop ALL --read-only --tmpfs /tmp:exec,size=512m \
		--tmpfs /home/agent:exec,size=64m,mode=0700,uid=1000,gid=1000 -e HOME=/home/agent \
		-v "$ws:/workspace" -v "$task_dir/controls/exploit.sh:/bench-exploit/exploit.sh:ro" -w /workspace \
		--entrypoint sh "$IMAGE" /bench-exploit/exploit.sh >"$out/exploit.log" 2>&1
	echo "exploit exit $?" >>"$out/exploit.log"
	set -e
	$run --user 0 --cap-drop ALL --cap-add DAC_OVERRIDE --cap-add DAC_READ_SEARCH --cap-add FOWNER \
		-v "$ws:/workspace:ro" -v "$out:/out" --entrypoint /bin/sh "$IMAGE" -c "$EXPORT"
	docker volume rm -f "$ws" >/dev/null
	echo "$out/exploit.diff"
}

# row TASK NAME WANT SUMMARY_LINE: prints one table row, counts mismatches.
FAILED=0
row() {
	task=$1; name=$2; want=$3; summary=$4
	got=fail
	case "$summary" in *'"pass":true'*) got=pass ;; esac
	ok=OK
	[ "$got" = "$want" ] || { ok=MISMATCH; FAILED=$((FAILED + 1)); }
	tests=$(printf '%s' "$summary" | sed -n 's/.*"tests":\([0-9]*\).*"failures":\([0-9]*\).*/\1 tests, \2 failed/p')
	[ -n "$tests" ] || tests=$(printf '%s' "$summary" | sed -n 's/.*"reason":"\([^"]*\)".*/\1/p')
	printf '| %-5s | %-16s | %-4s | %-4s | %-22s | %-8s |\n' "$task" "$name" "$want" "$got" "$tests" "$ok"
}

if [ "$#" -gt 0 ]; then TASKS="$*"; else
	TASKS=$(sed -n 's/^tasks: *\[\(.*\)\]/\1/p' "$SUITE/suite.yaml" | tr -d ' ' | tr ',' ' ')
fi

echo "image: $IMAGE ($(docker run --rm --network none --entrypoint node "$IMAGE" --version))"
printf '| %-5s | %-16s | %-4s | %-4s | %-22s | %-8s |\n' task control want got "hidden tests" result
printf '|%s|%s|%s|%s|%s|%s|\n' ------- ------------------ ------ ------ ------------------------ ----------
for task in $TASKS; do
	dir="$SUITE/tasks/$task"
	for i in 1 2 3; do
		row "$task" "reference_$i" pass "$(grade "$dir" "$task-ref$i" "$dir/solution/reference.diff" || true)"
	done
	row "$task" empty_patch fail "$(grade "$dir" "$task-empty" "$dir/controls/empty.diff" || true)"
	n=0
	for m in "$dir"/controls/mutant*.diff; do
		n=$((n + 1))
		row "$task" "mutant_$n" fail "$(grade "$dir" "$task-mutant$n" "$m" || true)"
	done
	diff=$(exploit_diff "$dir")
	row "$task" exploit fail "$(grade "$dir" "$task-exploit" "$diff" || true)"
	row "$task" reference+stale pass "$(grade "$dir" "$task-stale" "$dir/solution/reference.diff" "$dir/fixtures/notes.stale.md" || true)"
done

echo
if [ "$FAILED" -eq 0 ]; then echo "all checks as expected"; else echo "$FAILED check(s) not as expected"; exit 1; fi
