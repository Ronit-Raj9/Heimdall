"""Turn a verifier run into a Harbor reward (written by bifrost-bench export harbor).

Usage: check.py <result> <path> <exit code> [<required ids file> | <pass threshold>]

result is how the suite verifier reports (Heimdall's suite verifier.result):
  junit      pass only with exit 0, more than zero tests, no failures, errors
             or skips, and every required test id ran and passed (the same
             rule as Heimdall's hidden_tests grader, graders/junit.go)
  reward     a file holding a number or {"reward": n}; passes at the threshold
  exit_code  exit 0 passes
  answer     <path> is the answer file, the fourth argument the expected
             answer spec (JSON); match exact, contains_all, regex, numeric or choice

It prints the reward (1 or 0, or the reward itself for result reward) on
stdout and a one-line reason on stderr. A broken test run (no report, no
tests) is a fail, never an error, so an agent cannot turn a 0 into an
unknown by crashing the tests.
"""

import json
import re
import sys
import unicodedata
import xml.etree.ElementTree as ET


def fail(reason):
    print(reason, file=sys.stderr)
    print("0")
    sys.exit(0)


def cases_of(root):
    out = []
    counts = {"tests": 0, "failures": 0, "errors": 0, "skipped": 0}

    def tally(elems):
        t = {"tests": 0, "failures": 0, "errors": 0, "skipped": 0}
        for c in elems:
            failed = c.find("failure") is not None
            errored = c.find("error") is not None
            skipped = c.find("skipped") is not None
            out.append((c.get("classname", ""), c.get("name", ""), failed, errored, skipped))
            t["tests"] += 1
            t["failures"] += int(failed)
            t["errors"] += int(errored)
            t["skipped"] += int(skipped)
        return t

    def add_suite(s):
        t = tally(s.findall("testcase"))
        for k in counts:
            n = t[k]
            attr = s.get(k)
            if attr is not None:
                v = int(attr.strip())
                if v < 0:
                    raise ValueError("bad %s count" % k)
                n = max(v, n)
            counts[k] += n
        for nested in s.findall("testsuite"):
            add_suite(nested)

    if root.tag == "testsuites":
        top = tally(root.findall("testcase"))
        for k in counts:
            counts[k] += top[k]
        for s in root.findall("testsuite"):
            add_suite(s)
    elif root.tag == "testsuite":
        add_suite(root)
    else:
        raise ValueError("unexpected root element %r" % root.tag)
    return counts, out


def split_id(test_id):
    parts = test_id.split("::")
    if len(parts) < 2 or not parts[0] or not parts[-1]:
        raise ValueError("required test id %r is not file.py::test" % test_id)
    mod = parts[0][:-3] if parts[0].endswith(".py") else parts[0]
    mod = mod.strip("/").replace("/", ".")
    return ".".join([mod] + parts[1:-1]), parts[-1]


def class_matches(classname, want):
    classname = classname.replace("/", ".")
    return classname == want or classname.endswith("." + want)


def junit(path, code, required_file):
    try:
        data = open(path, "rb").read()
    except OSError:
        fail("no junit report (exit %d)" % code)
    if not data.strip():
        fail("no junit report (exit %d)" % code)
    try:
        counts, cases = cases_of(ET.fromstring(data))
    except (ET.ParseError, ValueError) as e:
        fail("junit report does not parse: %s" % e)
    if counts["tests"] == 0:
        fail("no tests ran (exit %d)" % code)
    required = []
    if required_file:
        required = [l.strip() for l in open(required_file, encoding="utf-8") if l.strip()]
    why = []
    if code != 0:
        why.append("exit %d" % code)
    for k in ("failures", "errors", "skipped"):
        if counts[k] > 0:
            why.append("%d %s" % (counts[k], k))
    for rid in required:
        cls, name = split_id(rid)
        ran = bad = skipped = False
        for (c_cls, c_name, c_failed, c_errored, c_skipped) in cases:
            if not class_matches(c_cls, cls) or not (c_name == name or c_name.startswith(name + "[")):
                continue
            if c_failed or c_errored:
                bad = True
            elif c_skipped:
                skipped = True
            else:
                ran = True
        if bad:
            why.append("required test failed: " + rid)
        elif skipped or not ran:
            why.append("required test did not run: " + rid)
    if why:
        fail("; ".join(why))
    print("passed (%d tests)" % counts["tests"], file=sys.stderr)
    print("1")


def reward(path, code, threshold):
    try:
        text = open(path, encoding="utf-8").read().strip()
    except OSError:
        fail("no reward file (exit %d)" % code)
    try:
        v = json.loads(text)
    except ValueError:
        fail("reward file does not parse")
    if isinstance(v, dict):
        v = v.get("reward")
    if isinstance(v, bool) or not isinstance(v, (int, float)):
        fail("reward file holds no number")
    print("reward %s, threshold %s" % (v, threshold), file=sys.stderr)
    print(v)


def normalize(s, names):
    # The fixed order of Heimdall's answer_match normalize (graders/answermatch.go).
    on = set(names or [])
    if "trim" in on:
        s = s.strip()
    if "lower" in on:
        s = s.lower()
    if "strip_punct" in on:
        s = "".join(ch for ch in s if not unicodedata.category(ch).startswith("P"))
    if "collapse_space" in on:
        s = " ".join(s.split())
    if "trim" in on:
        s = s.strip()
    return s


# Numeric and choice matching as Heimdall's answer_match reads them
# (graders/answermatch.go: numberRe, wordRe, choiceRe, answerPfx, numberWords).
NUMBER_RE = re.compile(r"[-+]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?", re.ASCII)
WORD_RE = re.compile(r"[a-z]+", re.ASCII)
CHOICE_RE = re.compile(r"^\(?([a-z]|\d{1,2})(?:[).:]|\s|$)", re.ASCII)
ANSWER_PFX = re.compile(r"^(?:the\s+)?(?:final\s+)?answer\s*(?:is|:)\s*", re.ASCII)
NUMBER_WORDS = {w: i for i, w in enumerate("zero one two three four five six seven eight nine ten eleven twelve thirteen "
                                           "fourteen fifteen sixteen seventeen eighteen nineteen twenty".split())}


def parse_number(s):
    v = float(s.strip().replace(",", ""))
    if v != v or v in (float("inf"), float("-inf")):
        raise ValueError("not a number")
    return v


def answer_numbers(s):
    low = s.lower()
    out = []
    for m in NUMBER_RE.findall(low):
        try:
            v = parse_number(m)
        except ValueError:
            continue
        if v not in out:
            out.append(v)
    for w in WORD_RE.findall(low):
        if w in NUMBER_WORDS and float(NUMBER_WORDS[w]) not in out:
            out.append(float(NUMBER_WORDS[w]))
    return out


def match_choice(got, expected, names):
    a = ANSWER_PFX.sub("", got.strip().lower(), count=1)
    m = CHOICE_RE.match(a)
    label = m.group(1) if m else ""
    both = ["lower", "trim"] + list(names)
    whole = normalize(a, both)
    for e in expected:
        want = e.strip().strip("().:").lower()
        if label and want == label:
            return True
        if whole == normalize(e, both):
            return True
    return False


def answer(path, spec_file):
    spec = json.load(open(spec_file, encoding="utf-8"))
    try:
        got = open(path, encoding="utf-8").read()
    except OSError:
        fail("no answer file at %s" % path)
    if got.startswith("﻿"):
        got = got[1:]
    if not got.strip():
        fail("answer file is empty")
    names = spec.get("normalize") or []
    match = spec["match"]
    expected = spec.get("expected") or []
    g = normalize(got, names)
    if match == "exact":
        ok = any(g == normalize(e, names) for e in expected)
    elif match == "contains_all":
        ok = all(normalize(e, names) in g for e in expected)
    elif match == "regex":
        # Go's RE2 and Python's re agree on the patterns the export allows.
        ok = any(re.search(e, g) for e in expected)
    elif match == "numeric":
        nums = answer_numbers(got)
        if not nums:
            fail("numeric: no number in the answer")
        if len(nums) > 1:
            fail("numeric: the answer states %d different numbers" % len(nums))
        tol = float(spec.get("tolerance") or 0)
        ok = any(abs(nums[0] - parse_number(e)) <= tol for e in expected)
    elif match == "choice":
        ok = match_choice(got, expected, names)
    else:
        print("answer match %r is not exported" % match, file=sys.stderr)
        sys.exit(3)
    if not ok:
        fail("answer does not match (%s)" % match)
    print("answer matches (%s)" % match, file=sys.stderr)
    print("1")


def main(argv):
    if len(argv) < 4:
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    kind, path, code = argv[1], argv[2], int(argv[3])
    extra = argv[4] if len(argv) > 4 else ""
    if kind == "junit":
        junit(path, code, extra)
    elif kind == "reward":
        reward(path, code, float(extra or 1))
    elif kind == "exit_code":
        if code != 0:
            fail("exit %d" % code)
        print("exit 0", file=sys.stderr)
        print("1")
    elif kind == "answer":
        answer(path, extra)
    else:
        print("unknown result kind %r" % kind, file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main(sys.argv)
