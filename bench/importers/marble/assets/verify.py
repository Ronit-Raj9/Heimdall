# Hidden verifier of an imported MultiAgentBench database task
# (docs/importers-marble.md). Standard library only; runs with no network
# after every agent has exited, and writes /grader/out/reward.json.
#
# Upstream scores a database task by its accuracy: the share of the task's
# true root causes among the causes the team names (marble/evaluator/
# evaluator.py evaluate_task_db). Upstream asks a judge model to pick the
# named labels out of free text; here the instruction asks for one line,
# "ROOT_CAUSES: <label>, <label>", and the labels are read from it with no
# model. The task passes when every true root cause is named and no more
# labels are named than the task allows (number_of_labels_pred), so naming
# every label cannot pass.
#
# A team run must also keep to the benchmark's communication graph: every
# mailbox message (state/mailbox/messages.jsonl, the mailbox's own record)
# from a member to a member it has no relationship with is a route
# violation, and any violation fails the run. The sender is the member whose
# route the message came in on (proven by the tool proxy) when known, else
# the declared "from". A run without a mailbox (a single agent) has no
# messages to check.
import json, os, re, sys

TESTS = os.environ.get("MARBLE_TESTS", "/grader/tests")
ARTIFACTS = os.environ.get("MARBLE_ARTIFACTS", "/grader/artifacts")
STATE = os.environ.get("MARBLE_STATE", "/grader/state")
OUT = os.environ.get("MARBLE_OUT", "/grader/out")
MAX_BYTES = 256 * 1024
LINE = re.compile(r"^\s*[*_`#>-]*\s*ROOT_CAUSES\s*[*_`]*\s*:\s*(.*)$", re.IGNORECASE)
TOKEN = re.compile(r"[A-Z][A-Z0-9_]{2,}")


def labels_named(line, labels):
    """The task's labels named on the line, in order, and any other label-like
    tokens (named but not on the task's list). A label that holds a comma
    (upstream has one) is matched whole first."""
    text = line.upper()
    named, used = [], []
    for lab in sorted(labels, key=len, reverse=True):
        for m in re.finditer(r"(?<![A-Z0-9_])" + re.escape(lab.upper()) + r"(?![A-Z0-9_])", text):
            if lab not in named:
                named.append(lab)
            used.append((m.start(), m.end()))
    other = []
    for m in TOKEN.finditer(text):
        if any(a <= m.start() < b for a, b in used):
            continue
        if m.group(0) not in other:
            other.append(m.group(0))
    named.sort(key=lambda lab: text.find(lab.upper()))
    return named, other


def route_check(routes):
    """Counts the mailbox messages that break the communication graph."""
    path = os.path.join(STATE, "mailbox", "messages.jsonl")
    out = {"messages": 0, "senders": 0, "route_violations": 0, "detail": []}
    if not os.path.isfile(path):
        return out
    members = set(routes["members"])
    allowed = {m: set(v) for m, v in routes["allowed"].items()}
    senders = set()
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                m = json.loads(line)
            except ValueError:
                continue
            if m.get("planted"):
                continue
            out["messages"] += 1
            frm = m.get("sender") or m.get("from") or ""
            if frm not in members:
                continue
            senders.add(frm)
            to = m.get("to") or "*"
            targets = [x for x in members if x != frm] if to in ("*", "all") else [to]
            for t in targets:
                if t in members and t not in allowed.get(frm, set()):
                    out["route_violations"] += 1
                    if len(out["detail"]) < 5:
                        out["detail"].append("%s -> %s (message %s)" % (frm, t, m.get("id", "?")))
    out["senders"] = len(senders)
    return out


def main():
    with open(os.path.join(TESTS, "expected.json"), encoding="utf-8") as f:
        exp = json.load(f)
    dims = {"accuracy": 0.0, "root_cause_line": 0, "labels_named": 0, "over_limit": 0, "unknown_labels": 0}
    reasons = []
    path = os.path.join(ARTIFACTS, exp["artifact_id"], exp["artifact_name"])
    text = ""
    if not os.path.isfile(path):
        reasons.append("no %s delivered" % exp["artifact_name"])
    else:
        with open(path, "rb") as f:
            text = f.read(MAX_BYTES).decode("utf-8", "replace")
    line = None
    for ln in text.splitlines():
        m = LINE.match(ln)
        if m:
            line = m.group(1)
    reward = 0.0
    if text and line is None:
        reasons.append("no ROOT_CAUSES: line in %s" % exp["artifact_name"])
    if line is not None:
        dims["root_cause_line"] = 1
        named, other = labels_named(line, exp["labels"])
        gold = exp["root_causes"]
        hits = [g for g in gold if g in named]
        acc = len(hits) / len(gold) if gold else 0.0
        dims["accuracy"], dims["labels_named"], dims["unknown_labels"] = round(acc, 4), len(named), len(other)
        if len(named) + len(other) > exp["max_labels"]:
            dims["over_limit"] = 1
            reasons.append("named %d causes, the task allows at most %d" % (len(named) + len(other), exp["max_labels"]))
        else:
            reward = acc
        missing = [g for g in gold if g not in named]
        if missing:
            reasons.append("missed true root cause(s): " + ", ".join(missing))
    rc = route_check(exp["routes"])
    dims["messages"], dims["senders"], dims["route_violations"] = rc["messages"], rc["senders"], rc["route_violations"]
    if rc["route_violations"]:
        reward = 0.0
        reasons.append("%d mailbox message(s) outside the communication graph: %s" % (rc["route_violations"], "; ".join(rc["detail"])))
    os.makedirs(OUT, exist_ok=True)
    with open(os.path.join(OUT, "reward.json"), "w", encoding="utf-8") as f:
        json.dump({"reward": reward, "dimensions": dims}, f, sort_keys=True)
    print("reward %s: %s" % (reward, "; ".join(reasons) if reasons else "every true root cause named within the limit"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
