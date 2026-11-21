# Hidden verifier of an imported CooperBench pair (docs/importing-cooperbench.md).
# Standard library only. Runs in a snapshot of the team's container after
# every member has exited, and writes /grader/out/reward.json.
#
# Upstream (cooperbench eval, sandbox.py) takes each agent's patch, merges
# them, and runs the task's runner.sh once per feature with that feature's
# tests.patch and the merged patch; a feature passes when runner.sh exits 0
# and the parsed test output shows at least one passed test; the pair passes
# when both features pass. Here the two members share one working tree, so
# the team's patch is the diff of that tree against the base commit (test
# files dropped, as upstream does), and the rest is upstream's procedure with
# upstream's runner.sh (a trusted copy, not the image's).
import json, os, re, shutil, subprocess, sys, tempfile

G = "/grader/tests"
OUT = "/grader/out"
SRC = "/workspace/repo"
SPEC = json.load(open(os.path.join(G, "spec.json")))
BASE = SPEC["base_commit"]
ENV = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", HOME=os.environ.get("HOME") or "/tmp",
           PIP_NO_INDEX="1", PIP_DISABLE_PIP_VERSION_CHECK="1", CARGO_NET_OFFLINE="true",
           GOFLAGS="-mod=mod", GOPROXY="off", UV_OFFLINE="1", CI="1")
LOG = []


def note(s):
    LOG.append(s)
    print("verify: " + s, flush=True)


def git(cwd, *args, env=None, check=True):
    r = subprocess.run(["git", "-c", "safe.directory=*", "-c", "core.hooksPath=/dev/null"] + list(args),
                       cwd=cwd, env=env or ENV, capture_output=True)
    if check and r.returncode != 0:
        raise RuntimeError("git %s: %s" % (" ".join(args[:2]), r.stderr.decode(errors="replace")[-400:]))
    return r.stdout


# Upstream's test-file filter (sandbox.py _TEST_DIR_RE, _TEST_FILE_RE).
TEST_DIR = re.compile(r"/(tests?|__tests__|spec|testdata)/")
TEST_FILE = re.compile(r"(^|/)(test_[^/]+|[^/]+_test\.[A-Za-z0-9]+|[^/]+\.(test|spec)\.[A-Za-z0-9]+|[^/]*Test[s]?\.(java|kt|cs|scala)|tests?\.py)$")


def is_test(header):
    return any(TEST_DIR.search("/" + p) or TEST_FILE.search(p) for p in re.findall(r"[ab]/(\S+)", header))


def filter_tests(patch):
    out, skip = [], False
    for line in patch.split("\n"):
        if line.startswith("diff --git"):
            skip = is_test(line)
        if not skip:
            out.append(line)
    s = "\n".join(out)
    if s and not s.endswith("\n"):
        s += "\n"
    return s


# Upstream's output parser (sandbox.py _parse_results).
def parse(output):
    m = re.search(r"Tests:\s*(?:(\d+)\s*failed,\s*)?(\d+)\s*passed", output)
    if m:
        return int(m.group(2)), int(m.group(1) or 0)
    p = re.search(r"(\d+) passed", output)
    f = re.search(r"(\d+) failed", output)
    e = re.search(r"(\d+) error", output)
    passed = int(p.group(1)) if p else 0
    failed = (int(f.group(1)) if f else 0) + (int(e.group(1)) if e else 0)
    if passed or failed:
        return passed, failed
    gp, gf = len(re.findall(r"--- PASS:", output)), len(re.findall(r"--- FAIL:", output))
    if gp or gf:
        return gp, gf
    ok = len(re.findall(r"^ok\s+\S+", output, re.M))
    bad = len(re.findall(r"^FAIL\s+\S+", output, re.M))
    if ok or bad:
        return (ok if bad == 0 else 0), bad
    m = re.search(r"test result:.*?(\d+) passed.*?(\d+) failed", output)
    if m:
        return int(m.group(1)), int(m.group(2))
    return 0, 0


def team_patch(src):
    idx = tempfile.mktemp(prefix="cb-index-")
    env = dict(ENV, GIT_INDEX_FILE=idx)
    git(src, "read-tree", BASE, env=env)
    git(src, "add", "-A", "--", ".", ":(exclude)patch.txt", env=env)
    return git(src, "diff", "--cached", "--binary", BASE, env=env).decode("utf-8", errors="surrogateescape")


INSTALL = re.compile(r"^\s*(uv pip install|pip install|pip3 install|python3? -m pip install|pnpm install|npm install|npm ci|yarn install|uv sync|uv venv)\b")


def runner_copy(work, patches):
    text = open(os.path.join(G, "runner.sh"), encoding="utf-8").read()
    text = text.replace("/workspace/repo", work).replace("/patches", patches)
    # The verifier has no network: the image already holds every
    # dependency, so an install step that fails offline must not abort.
    lines = []
    for line in text.split("\n"):
        if INSTALL.match(line) and not line.rstrip().endswith("\\"):
            line = line.rstrip() + " || true"
        lines.append(line)
    path = os.path.join(patches, "runner.sh")
    open(path, "w", encoding="utf-8").write("\n".join(lines))
    return path


def run_feature(work, patches, n, patch):
    for lock in (".git/index.lock", ".git/refs/heads/.lock"):
        try:
            os.remove(os.path.join(work, lock))
        except OSError:
            pass
    git(work, "checkout", "--force", BASE, check=False)
    git(work, "reset", "--hard", BASE)
    git(work, "clean", "-fd", check=False)
    shutil.copy(os.path.join(G, "tests%d.patch" % n), os.path.join(patches, "tests.patch"))
    open(os.path.join(patches, "agent.patch"), "w", encoding="utf-8", errors="surrogateescape").write(patch)
    runner = runner_copy(work, patches)
    try:
        r = subprocess.run(["bash", runner, "tests.patch", "agent.patch"], cwd=work, env=ENV,
                           capture_output=True, timeout=SPEC.get("feature_timeout_s", 1200))
        out = (r.stdout + r.stderr).decode(errors="replace")
        rc = r.returncode
    except subprocess.TimeoutExpired as e:
        out = ((e.stdout or b"") + (e.stderr or b"")).decode(errors="replace") + "\nTIMEOUT"
        rc = 124
    with open(os.path.join(OUT, "feature%d.log" % n), "w") as f:
        f.write(out[-200000:])
    p, fl = parse(out)
    return {"passed": rc == 0 and p > 0, "exit_code": rc, "tests_passed": p, "tests_failed": fl}


def main():
    os.makedirs(OUT, exist_ok=True)
    dims = {}
    reward = 0
    try:
        src = SRC
        try:
            patch = team_patch(src)
        except Exception as e:  # read-only snapshot: work on a copy
            note("diff in place failed (%s); copying the workspace" % e)
            src = tempfile.mkdtemp(prefix="cb-src-") + "/repo"
            shutil.copytree(SRC, src, symlinks=True)
            patch = team_patch(src)
        patch = filter_tests(patch)
        dims["patch_bytes"] = len(patch)
        if not patch.strip():
            note("the team changed no source file")
            results = {n: {"passed": False, "exit_code": None, "tests_passed": 0, "tests_failed": 0} for n in (1, 2)}
        else:
            patches = tempfile.mkdtemp(prefix="cb-patches-")
            results = {n: run_feature(src, patches, n, patch) for n in (1, 2)}
        for n, r in results.items():
            dims["feature%d_passed" % n] = 1 if r["passed"] else 0
            dims["feature%d_tests_passed" % n] = r["tests_passed"]
            dims["feature%d_tests_failed" % n] = r["tests_failed"]
        reward = 1 if all(r["passed"] for r in results.values()) else 0
        note("features %s: feature %d %s, feature %d %s" % (SPEC["pair"], SPEC["features"][0], "pass" if results[1]["passed"] else "fail",
                                                        SPEC["features"][1], "pass" if results[2]["passed"] else "fail"))
    except Exception as e:
        note("verifier error: %s" % e)
        reward = 0
        dims["verifier_error"] = 1
    with open(os.path.join(OUT, "reward.json"), "w") as f:
        json.dump({"reward": reward, "dimensions": dims}, f, sort_keys=True)
    with open(os.path.join(OUT, "verify.log"), "a") as f:
        f.write("\n".join(LOG) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
