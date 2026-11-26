package swebench

// The verifier files every imported task carries in verifier/tests, beside
// its own test.patch, test_files, base_commit, cmd.sh and tests.json. The
// suite's verifier command runs verify.sh in the task image with the
// workspace (pristine repository plus the agent's diff) read-only at the
// working directory and no network.

// verifyScript copies the workspace to /tmp (or works in place when the
// mount is writable), resets the test patch's files and every conftest.py,
// pytest.ini and tox.ini to the base commit (as upstream's eval script checks
// the test files out before applying the test patch, and so that no file the
// agent planted can rewrite test results), applies the test patch, runs
// cmd.sh with the instance's environment active (and, on a copy, the copy
// first on PYTHONPATH) and has grade.py write
// /grader/out/junit.xml. It exits 0 once the report is written: the report,
// not the test command's exit code, carries the result. Only a broken
// verifier exits non-zero.
const verifyScript = `#!/bin/bash
# Clean-room verifier of an imported SWE-bench-style task. See
# docs/importing-swe.md.
G=/grader/tests
OUT=/grader/out
SRC=$(pwd)
BASE=$(cat "$G/base_commit")
W=/tmp/bench-w
export HOME="${HOME:-/tmp}" PYTHONDONTWRITEBYTECODE=1 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
note() { echo "verify: $*" >> "$OUT/verify.log"; }
git_src() { git -c safe.directory='*' -C "$SRC" "$@"; }

PY=""
for p in /opt/miniconda3/bin/python3 /usr/bin/python3 /usr/local/bin/python3 "$(command -v python3 2>/dev/null)" "$(command -v python 2>/dev/null)"; do
	if [ -n "$p" ] && [ -x "$p" ]; then PY=$p; break; fi
done
if [ -z "$PY" ]; then echo "verify: no python to run grade.py" >&2; exit 3; fi
grade() { "$PY" -I -S -B "$G/grade.py" --spec "$G/tests.json" --out "$OUT/junit.xml" "$@"; }

if [ -w "$SRC" ]; then
	W=$SRC
else
	mkdir -p "$W" || exit 3
	if ! tar -C "$SRC" --exclude=./.git -cf - . | tar -C "$W" -xf -; then
		note "copy of the workspace to $W failed"
		grade --broken "workspace copy failed" || exit 3
		exit 0
	fi
fi

reset_path() {
	if git_src cat-file -e "$BASE:$1" 2>/dev/null; then
		mkdir -p "$(dirname "$W/$1")" && git_src show "$BASE:$1" > "$W/$1"
	else
		rm -f "$W/$1"
	fi
}
if command -v git >/dev/null 2>&1 && git_src cat-file -e "$BASE^{commit}" 2>/dev/null; then
	while IFS= read -r f; do [ -n "$f" ] && reset_path "$f"; done < "$G/test_files"
	{ (cd "$W" && find . -path ./.git -prune -o \( -name conftest.py -o -name pytest.ini -o -name tox.ini \) -type f -print | sed 's|^\./||')
	  git_src ls-tree -r --name-only "$BASE" | grep -E '(^|/)(conftest\.py|pytest\.ini|tox\.ini)$'; } | sort -u | while IFS= read -r f; do reset_path "$f"; done
else
	note "base commit $BASE is not readable in the workspace: test files and conftest.py were not reset"
fi

cd "$W" || exit 3
if ! git -c safe.directory='*' apply --whitespace=nowarn "$G/test.patch" 2>> "$OUT/verify.log"; then
	if ! patch -p1 --forward --batch < "$G/test.patch" >> "$OUT/verify.log" 2>&1; then
		note "test patch does not apply"
		grade --broken "test patch does not apply" || exit 3
		exit 0
	fi
fi

if [ -f /opt/miniconda3/bin/activate ]; then
	. /opt/miniconda3/bin/activate testbed > /dev/null 2>&1 || . /opt/miniconda3/bin/activate > /dev/null 2>&1
fi
# The copy, not the read-only workspace, is the code under test: the image
# installs the repository in development mode from the workspace path, so
# without this a test module inside the package is imported from there (old
# test files, ImportPathMismatchError against the copy's conftest.py).
if [ "$W" != "$SRC" ]; then
	export PYTHONPATH="$W${PYTHONPATH:+:$PYTHONPATH}"
fi
# A test command that writes a JUnit report (pytest --junitxml="$BENCH_JUNIT",
# as tasks built from issues do) is graded from that report first.
export BENCH_JUNIT=/tmp/bench-junit.xml
rm -f "$BENCH_JUNIT"
bash "$G/cmd.sh" > /tmp/bench-test.log 2>&1
code=$?
tail -c 1000000 /tmp/bench-test.log > "$OUT/test.log"
grade --log /tmp/bench-test.log --exit "$code" --junit "$BENCH_JUNIT" || exit 3
exit 0
`

// gradeScript reads the test log with the instance's log parser (each a
// re-implementation of the upstream SWE-bench parser of that name) and writes
// one JUnit testcase per FAIL_TO_PASS and PASS_TO_PASS test, under the names
// tests.json gives, or one testcase for the test command's exit status when
// the task follows upstream's exit-code rule. A test passes only when the log
// shows it PASSED or XFAIL; a test missing from the log fails, as upstream
// counts it. When the test command also wrote a JUnit XML report
// (--junit), its results are read too and win over the log for the tests
// it names, matched to upstream ids by file path and pytest node id.
const gradeScript = `"""Turn a SWE-bench-style test log into the JUnit report the bench reads.

Usage: grade.py --spec tests.json --out junit.xml (--log FILE --exit N [--junit FILE] | --broken REASON)
"""

import argparse
import json
import os
import re
import xml.etree.ElementTree as ET
from xml.sax.saxutils import quoteattr

PASSED, FAILED, SKIPPED, ERROR, XFAIL = "PASSED", "FAILED", "SKIPPED", "ERROR", "XFAIL"
STATUSES = (FAILED, PASSED, SKIPPED, ERROR, XFAIL)


def _summary_line(line, status_map):
    if line.startswith(FAILED):
        line = line.replace(" - ", " ")
    parts = line.split()
    if len(parts) > 1:
        status_map[parts[1]] = parts[0]


def parse_pytest(log):
    status_map = {}
    for line in log.split("\n"):
        if line.startswith(STATUSES):
            _summary_line(line, status_map)
    return status_map


def parse_pytest_options(log):
    option = re.compile(r"(.*?)\[(.*)\]")
    status_map = {}
    for line in log.split("\n"):
        if not line.startswith(STATUSES):
            continue
        if line.startswith(FAILED):
            line = line.replace(" - ", " ")
        parts = line.split()
        if len(parts) <= 1:
            continue
        name = parts[1]
        m = option.search(name)
        if m:
            main, opt = m.groups()
            if opt.startswith("/") and not opt.startswith("//") and "*" not in opt:
                opt = "/" + opt.split("/")[-1]
            name = main + "[" + opt + "]"
        status_map[name] = parts[0]
    return status_map


def parse_pytest_v2(log):
    controls = str.maketrans("", "", "".join(chr(c) for c in range(1, 32)))
    status_map = {}
    for line in log.split("\n"):
        line = re.sub(r"\[(\d+)m", "", line).translate(controls)
        if line.startswith(STATUSES):
            if line.startswith(FAILED):
                line = line.replace(" - ", " ")
            parts = line.split()
            if len(parts) >= 2:
                status_map[parts[1]] = parts[0]
        elif line.endswith(STATUSES):
            parts = line.split()
            if len(parts) >= 2:
                status_map[parts[0]] = parts[1]
    return status_map


def parse_matplotlib(log):
    status_map = {}
    for line in log.split("\n"):
        line = line.replace("MouseButton.LEFT", "1").replace("MouseButton.RIGHT", "3")
        if line.startswith(STATUSES):
            _summary_line(line, status_map)
    return status_map


def parse_seaborn(log):
    status_map = {}
    for line in log.split("\n"):
        parts = line.split()
        if line.startswith(FAILED) and len(parts) > 1:
            status_map[parts[1]] = FAILED
        elif " " + PASSED + " " in line and len(parts) > 1 and parts[1] == PASSED:
            status_map[parts[0]] = PASSED
        elif line.startswith(PASSED) and len(parts) > 1:
            status_map[parts[1]] = PASSED
    return status_map


def parse_sympy(log):
    status_map = {}
    for m in re.findall(r"(_*) (.*)\.py:(.*) (_*)", log):
        status_map[m[1] + ".py:" + m[2]] = FAILED
    for line in log.split("\n"):
        line = line.strip()
        if not line.startswith("test_"):
            continue
        name = line.split()[0]
        if line.endswith(" E"):
            status_map[name] = ERROR
        if line.endswith(" F"):
            status_map[name] = FAILED
        if line.endswith(" ok"):
            status_map[name] = PASSED
    return status_map


def parse_django(log):
    status_map = {}
    prev = None
    for line in log.split("\n"):
        line = line.strip()
        if "--version is equivalent to version" in line:
            status_map["--version is equivalent to version"] = PASSED
        if " ... " in line:
            prev = line.split(" ... ")[0]
        for suffix in (" ... ok", " ... OK", " ...  OK"):
            if line.endswith(suffix):
                if line.startswith("Applying sites.0002_alter_domain_unique...test_no_migrations"):
                    line = line.split("...", 1)[-1].strip()
                status_map[line.rsplit(suffix, 1)[0]] = PASSED
                break
        if " ... skipped" in line:
            status_map[line.split(" ... skipped")[0]] = SKIPPED
        if line.endswith(" ... FAIL"):
            status_map[line.split(" ... FAIL")[0]] = FAILED
        if line.startswith("FAIL:") and len(line.split()) > 1:
            status_map[line.split()[1].strip()] = FAILED
        if line.endswith(" ... ERROR"):
            status_map[line.split(" ... ERROR")[0]] = ERROR
        if line.startswith("ERROR:") and len(line.split()) > 1:
            status_map[line.split()[1].strip()] = ERROR
        if line.lstrip().startswith("ok") and prev is not None:
            status_map[prev] = PASSED
    for pattern in (
        r"^(.*?)\s\.\.\.\sTesting\ against\ Django\ installed\ in\ ((?s:.*?))\ silenced\)\.\nok$",
        r"^(.*?)\s\.\.\.\sInternal\ Server\ Error:\ \/(.*)\/\nok$",
        r"^(.*?)\s\.\.\.\sSystem check identified no issues \(0 silenced\)\nok$",
    ):
        for m in re.finditer(pattern, log, re.MULTILINE):
            status_map[m.group(1)] = PASSED
    return status_map


def junit_ids(classname, name, file):
    """Every pytest node id a JUnit testcase can stand for."""
    ids = set()
    ids.add(classname + "::" + name)
    parts = [p for p in classname.split(".") if p]
    if file:
        file = file.replace("\\", "/")
        mod = file[:-3].replace("/", ".") if file.endswith(".py") else ""
        rest = parts
        if mod and classname.startswith(mod):
            rest = [p for p in classname[len(mod):].split(".") if p]
        ids.add("::".join([file] + rest + [name]))
    for k in range(1, len(parts) + 1):
        ids.add("::".join(["/".join(parts[:k]) + ".py"] + parts[k:] + [name]))
    if not parts:
        ids.add(name)
    return ids


def parse_junit(path):
    """Status per pytest node id from a JUnit XML report, or {} when absent."""
    if not path or not os.path.isfile(path):
        return {}
    try:
        root = ET.parse(path).getroot()
    except (ET.ParseError, OSError, ValueError):
        return {}
    status_map = {}
    for case in root.iter("testcase"):
        tags = {child.tag for child in case}
        if "failure" in tags:
            status = FAILED
        elif "error" in tags:
            status = ERROR
        elif "skipped" in tags:
            skipped = case.find("skipped")
            kind = (skipped.get("type") or "") + (skipped.get("message") or "")
            status = XFAIL if "xfail" in kind.lower() else SKIPPED
        else:
            status = PASSED
        for i in junit_ids(case.get("classname") or "", case.get("name") or "", case.get("file") or ""):
            # A failing duplicate (call passed, teardown errored) wins.
            if status_map.get(i) not in (FAILED, ERROR):
                status_map[i] = status
    return status_map


PARSERS = {
    "parse_log_pytest": parse_pytest,
    "parse_log_pytest_options": parse_pytest_options,
    "parse_log_pytest_v2": parse_pytest_v2,
    "parse_log_matplotlib": parse_matplotlib,
    "parse_log_seaborn": parse_seaborn,
    "parse_log_sympy": parse_sympy,
    "parse_log_django": parse_django,
}


def cases_for(spec, args):
    tests = spec["tests"]
    if args.broken:
        return [(t, "error", args.broken) for t in tests]
    if spec["rule"] == "exit_code":
        ok = args.exit == 0
        return [(t, None if ok else "failure", "test command exited %d" % args.exit) for t in tests]
    with open(args.log, encoding="utf-8", errors="replace") as f:
        status_map = PARSERS[spec["log_parser"]](f.read())
    status_map.update(parse_junit(args.junit))
    out = []
    for t in tests:
        status = status_map.get(t["id"])
        if status in (PASSED, XFAIL):
            out.append((t, None, ""))
        elif status is None:
            out.append((t, "failure", "not found in the test log"))
        else:
            out.append((t, "failure", "status " + status))
    return out


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--spec", required=True)
    p.add_argument("--out", required=True)
    p.add_argument("--log")
    p.add_argument("--exit", type=int, default=0)
    p.add_argument("--broken")
    p.add_argument("--junit")
    args = p.parse_args()
    with open(args.spec, encoding="utf-8") as f:
        spec = json.load(f)
    cases = cases_for(spec, args)
    failures = sum(1 for c in cases if c[1] == "failure")
    errors = sum(1 for c in cases if c[1] == "error")
    rows = []
    for t, kind, message in cases:
        head = "<testcase classname=%s name=%s>" % (quoteattr(t["kind"]), quoteattr(t["name"]))
        if kind:
            rows.append("%s<%s message=%s/></testcase>" % (head, kind, quoteattr(message)))
        else:
            rows.append(head + "</testcase>")
    with open(args.out, "w", encoding="utf-8") as f:
        f.write('<?xml version="1.0" encoding="utf-8"?>\n')
        f.write('<testsuites><testsuite name="swebench" tests="%d" failures="%d" errors="%d" skipped="0">\n' % (len(cases), failures, errors))
        f.write("\n".join(rows))
        f.write("\n</testsuite></testsuites>\n")


if __name__ == "__main__":
    main()
`
