"""Grade one MCPMark filesystem task offline.

Written by `bifrost-bench import mcpmark`. The verifier container gets the
filesystem service's exported /data folder at /grader/state/fs/data (a tar,
or a folder) and the task's hidden files at /grader/tests. This script
unpacks the state into a scratch folder and runs the upstream verify.py
against it the way MCPMark does: with FILESYSTEM_TEST_DIR naming the test
environment. It writes /grader/out/reward.json: 1 when verify.py exits 0,
else 0. When the state is missing it writes no reward, so the grade is
undecided rather than a failure.
"""

import json
import os
import subprocess
import sys
import tarfile
import tempfile

STATE = "/grader/state/fs/data"
TESTS = "/grader/tests"
OUT = "/grader/out"


def unpack(src, dst):
    """Return the folder holding the test environment."""
    if os.path.isdir(src):
        return src
    with tarfile.open(src) as tf:
        if hasattr(tarfile, "data_filter"):
            tf.extractall(dst, filter="data")
        else:
            tf.extractall(dst)
    entries = os.listdir(dst)
    if len(entries) == 1 and os.path.isdir(os.path.join(dst, entries[0])) and entries[0] == "data":
        return os.path.join(dst, "data")
    return dst


def main():
    os.makedirs(OUT, exist_ok=True)
    if not os.path.exists(STATE):
        print("state export missing: " + STATE, file=sys.stderr)
        return 2
    root = unpack(STATE, tempfile.mkdtemp(prefix="mcpmark-fs-"))
    env = dict(os.environ, FILESYSTEM_TEST_DIR=root, PYTHONDONTWRITEBYTECODE="1")
    with open(os.path.join(OUT, "verify.log"), "w") as log:
        try:
            proc = subprocess.run(
                [sys.executable, os.path.join(TESTS, "verify.py")],
                env=env, stdout=log, stderr=subprocess.STDOUT, timeout=300,
            )
            code = proc.returncode
        except subprocess.TimeoutExpired:
            print("verify.py timed out", file=log)
            code = 124
    reward = 1.0 if code == 0 else 0.0
    with open(os.path.join(OUT, "reward.json"), "w") as f:
        json.dump({"reward": reward, "dimensions": {"verify_exit_code": code}}, f)
    print("verify.py exit %d, reward %.0f" % (code, reward))
    return 0


if __name__ == "__main__":
    sys.exit(main())
