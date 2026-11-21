#!/bin/bash
# ProgramBench grading, converted from upstream's eval pipeline
# (programbench/eval/eval.py at the pinned commit) by bifrost-bench import
# programbench. Upstream extracts the submission into a fresh task image; the
# bench grades a snapshot of the agent's container instead, so this script
# repeats upstream's steps in place:
#   1. delete every workspace file whose sha256 is a copy of the original
#      binary (task.yaml eval_clean_hashes) and any ./executable;
#   2. seed a git repository when there is none (as upstream does);
#   3. run ./compile.sh (no network: the verifier has none) and stash
#      ./executable;
#   4. for every non-ignored hidden test branch: extract its archive into
#      the workspace, restore the executable, run eval/run.sh and keep
#      eval/results.xml;
#   5. score = passed tests / expected tests (tests.json minus ignored
#      tests), upstream's `programbench info` score. reward.txt holds it;
#      a task counts as resolved only at 1.
set -u
# The hidden tests' eval/run.sh installs its pytest plugins at run time;
# the verifier has no network, so this line moves to the image build
# (bifrost-bench import harbor bakes package installs), pinned.
pip install -q pytest-timeout==2.4.0 pytest-xdist==3.8.0 pytest-rerunfailures==16.4
mkdir -p /logs/verifier/pb
echo 0 > /logs/verifier/reward.txt
cd /workspace || exit 0
if [ -s /tests/clean_hashes.txt ]; then
	find /workspace -type f -exec sha256sum {} + 2>/dev/null | grep -F -f /tests/clean_hashes.txt | cut -c67- | while IFS= read -r f; do rm -f "$f"; done
fi
rm -f ./executable
if [ ! -d .git ]; then
	GIT_AUTHOR_DATE='2000-01-01T00:00:00Z' GIT_COMMITTER_DATE='2000-01-01T00:00:00Z' git -c init.defaultBranch=gold init -q &&
	git -c user.email=gold@local -c user.name=gold -c commit.gpgsign=false add -A &&
	GIT_AUTHOR_DATE='2000-01-01T00:00:00Z' GIT_COMMITTER_DATE='2000-01-01T00:00:00Z' git -c user.email=gold@local -c user.name=gold -c commit.gpgsign=false commit -q --allow-empty -m gold
fi
if [ ! -f ./compile.sh ]; then
	echo "no ./compile.sh in the workspace" > /logs/verifier/pb/compile.log
	exit 0
fi
chmod +x ./compile.sh
timeout 900 ./compile.sh > /logs/verifier/pb/compile.log 2>&1
echo "compile.sh exit $?" >> /logs/verifier/pb/compile.log
if [ ! -f ./executable ]; then
	echo "compile.sh produced no ./executable" >> /logs/verifier/pb/compile.log
	exit 0
fi
STASH=/opt/programbench-stashed-executable-do-not-modify
mkdir -p /opt && mv ./executable "$STASH"
for tarball in /tests/branches/*.tar.gz; do
	[ -f "$tarball" ] || continue
	b=$(basename "$tarball" .tar.gz)
	tar -tzf "$tarball" | sed -e 's#^\./##' -e 's#/.*##' | sort -u | while IFS= read -r top; do
		[ -n "$top" ] && [ "$top" != "." ] && rm -rf "/workspace/$top"
	done
	tar -xzf "$tarball" -C /workspace
	cp "$STASH" ./executable && chmod +x ./executable
	rm -f eval/results.xml results.xml
	if [ -f eval/run.sh ]; then
		sed -i 's/--timeout-method=thread/--timeout-method=signal/g' eval/run.sh
		chmod +x eval/run.sh
		PYTEST_ADDOPTS="--reruns=2 --reruns-delay=1" timeout 1500 ./eval/run.sh > "/logs/verifier/pb/$b.log" 2>&1
	fi
	[ -f eval/results.xml ] && cp eval/results.xml "/logs/verifier/pb/$b.xml"
done
python3 /tests/grade.py /tests/tests.json /logs/verifier/pb > /logs/verifier/reward.txt.new && mv /logs/verifier/reward.txt.new /logs/verifier/reward.txt
cat /logs/verifier/reward.txt
