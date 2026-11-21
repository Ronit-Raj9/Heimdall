"""ProgramBench score: passed / expected tests over the non-ignored branches.

Mirrors upstream's `programbench info`: a test listed in tests.json that has
no result counts as not run (failed); ignored branches and ignored tests are
left out; only status "passed" (a testcase with no failure, error or skip
child) counts. Prints the score as a bare decimal.
"""
import json
import os
import sys
import xml.etree.ElementTree as ET


def passed_names(path, branch):
    out = set()
    try:
        root = ET.parse(path).getroot()
    except Exception:
        return out
    for case in root.iter("testcase"):
        name = case.get("name") or ""
        cls = case.get("classname") or ""
        full = f"{cls}.{name}" if cls else name
        if not full:
            continue
        kinds = {c.tag for c in case if c.tag in ("failure", "error", "skipped")}
        if not kinds:
            out.add(f"{branch}/{full}")
    return out


def main():
    tests = json.load(open(sys.argv[1]))
    results = sys.argv[2]
    expected, passed = set(), set()
    for branch, info in (tests.get("branches") or {}).items():
        if info.get("ignored"):
            continue
        ignored = {t["name"] for t in info.get("ignored_tests") or []}
        for name in info.get("tests") or []:
            if name not in ignored:
                expected.add(f"{branch}/{name}")
        xml = os.path.join(results, branch + ".xml")
        if os.path.exists(xml):
            passed |= passed_names(xml, branch)
    score = len(expected & passed) / len(expected) if expected else 0.0
    json.dump({"expected": len(expected), "passed": len(expected & passed)}, open(os.path.join(results, "score.json"), "w"))
    print(f"{score:.6f}".rstrip("0").rstrip(".") if score not in (0, 1) else str(int(score)))


main()
