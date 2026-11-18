# Reference control of an imported MultiAgentBench database task
# (docs/importers-marble.md). It runs in the agent's place: once at
# admission (one container, no team), and as every member of a scripted
# reference team (the oracle on a task that declares a team). Standard
# library only; no model call. The plan (JSON) comes on stdin.
#
# Alone, it queries the database tool once and writes the deliverable. In a
# team, every member queries the database tool on its own route, so the
# tool proxy stamps the call with it; each worker sends its finding about
# the root cause its profile assigns it to the lead through the mailbox,
# along the shortest path of the communication graph (members relay what
# is not for them), and only to members it has a relationship with. The lead
# waits for every member's finding, writes the deliverable and sends "done"
# along the graph.
import json, os, sys, time, urllib.request

PLAN = json.load(sys.stdin)
ME = os.environ.get("HEIMDALL_AGENT_ID", "")
MAILBOX = os.environ.get("HEIMDALL_MAILBOX_URL", "").rstrip("/")
WS = os.environ.get("BENCH_WORKSPACE", "/workspace")
WAIT_S = float(PLAN.get("wait_s", 150))


def post(url, obj, timeout=30):
    req = urllib.request.Request(url, data=json.dumps(obj).encode(), method="POST",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.load(r)


def tool_url():
    if os.environ.get("MARBLE_TOOL_URL"):  # tests only
        return os.environ["MARBLE_TOOL_URL"].rstrip("/")
    if MAILBOX and MAILBOX.endswith("/s/mailbox"):
        return MAILBOX[: -len("mailbox")] + PLAN["tool"]
    return "http://toolproxy:9200/s/" + PLAN["tool"]


def probe():
    """One query on the database tool: the reference must reach it."""
    last = ""
    for _ in range(10):
        try:
            res = post(tool_url() + "/query", {"sql": PLAN["probe_sql"]})
            if res.get("status") == "success":
                rows = res.get("rows") or [[""]]
                return str(rows[0][0])
            last = res.get("explanation", "")
        except Exception as e:  # the tool may still be starting
            last = str(e)
        time.sleep(1)
    print("database tool unreachable: %s" % last, file=sys.stderr)
    sys.exit(1)


def write_deliverable(findings):
    lines = ["# Diagnosis", "", "ROOT_CAUSES: " + ", ".join(PLAN["root_causes"]), "", "## Findings", ""]
    for m in PLAN["members"]:
        f = findings.get(m)
        if f:
            lines.append("- %s checked %s: %s (pg_stat_statements rows seen: %s)" % (
                m, f.get("label") or "its share", "a root cause" if f.get("present") else "not a root cause", f.get("rows", "?")))
    with open(os.path.join(WS, PLAN["artifact"]), "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines) + "\n")


def my_finding(rows):
    f = dict(PLAN["findings"].get(ME) or {})
    f["rows"] = rows
    return f


def send(to, body):
    if to not in PLAN["allowed"].get(ME, []):
        raise SystemExit("refusing to message %s: no relationship from %s" % (to, ME))
    post(MAILBOX + "/send", {"from": ME, "to": to, "body": json.dumps(body)})


def recv():
    try:
        out = []
        for m in post(MAILBOX + "/recv", {"agent": ME}).get("messages", []):
            try:
                out.append(json.loads(m.get("body", "")))
            except ValueError:
                pass
        return out
    except Exception as e:
        print("recv failed: %s" % e, file=sys.stderr)
        return []


def next_hop():
    return PLAN["paths"][ME][1]


def children():
    """The members whose shortest path to the lead goes through me."""
    return [m for m in PLAN["members"] if m != ME and len(PLAN["paths"].get(m, [])) > 1 and PLAN["paths"][m][1] == ME]


def spread_done():
    for c in children():
        send(c, {"kind": "done", "origin": PLAN["lead"]})


def lead(rows):
    findings = {ME: my_finding(rows)}
    want = [m for m in PLAN["members"] if m != ME]
    deadline = time.time() + WAIT_S
    while time.time() < deadline and any(m not in findings for m in want):
        for b in recv():
            if b.get("kind") == "finding" and b.get("origin") in want:
                findings[b["origin"]] = b
        time.sleep(0.2)
    if any(m not in findings for m in want):
        print("lead %s: findings missing from %s" % (ME, [m for m in want if m not in findings]), file=sys.stderr)
        return 1
    write_deliverable(findings)
    spread_done()
    print("lead %s: wrote %s from %d findings" % (ME, PLAN["artifact"], len(findings)))
    return 0


def worker(rows):
    f = my_finding(rows)
    f.update({"kind": "finding", "origin": ME})
    send(next_hop(), f)
    relayed = {ME}
    deadline = time.time() + WAIT_S
    while time.time() < deadline:
        for b in recv():
            if b.get("kind") == "done":
                spread_done()
                print("worker %s: done" % ME)
                return 0
            if b.get("kind") == "finding" and b.get("origin") not in relayed:
                relayed.add(b["origin"])
                send(next_hop(), b)
        time.sleep(0.2)
    print("worker %s: no done from the lead" % ME, file=sys.stderr)
    return 1


if __name__ == "__main__":
    rows = probe()
    if not MAILBOX or not ME:
        write_deliverable({m: dict(PLAN["findings"].get(m) or {}, rows=rows) for m in PLAN["members"]})
        print("reference (alone): wrote %s" % PLAN["artifact"])
        sys.exit(0)
    sys.exit(lead(rows) if ME == PLAN["lead"] else worker(rows))
