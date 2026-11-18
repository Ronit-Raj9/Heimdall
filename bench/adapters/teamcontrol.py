# Scripted team control (docs/multi-agent.md): every member of a team runs
# this script. The lead (HEIMDALL_AGENT_ROLE=lead) sends one task to every
# other member through the team mailbox, waits for every reply and writes
# TEAM_RESULT.md; any other role is a worker that waits for its task, writes
# <its id>.txt and replies to the lead. Deterministic, no model calls.
import json, os, sys, time, urllib.request

ME = os.environ.get("HEIMDALL_AGENT_ID", "")
ROLE = os.environ.get("HEIMDALL_AGENT_ROLE", "worker")
TEAM = [m for m in os.environ.get("HEIMDALL_TEAM", "").split(",") if m]
URL = os.environ.get("HEIMDALL_MAILBOX_URL", "").rstrip("/")
WS = os.environ.get("BENCH_WORKSPACE", "/workspace")
WAIT_S = float(os.environ.get("HEIMDALL_TEAM_WAIT_S", "90"))


def post(path, obj):
    req = urllib.request.Request(URL + path, data=json.dumps(obj).encode(), method="POST",
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.load(r)


def recv():
    try:
        return post("/recv", {"agent": ME}).get("messages", [])
    except Exception as e:  # the mailbox may still be starting
        print("recv failed: %s" % e, file=sys.stderr)
        return []


def lead():
    workers = [m for m in TEAM if m != ME]
    for w in workers:
        post("/send", {"from": ME, "to": w, "body": "write %s.txt naming yourself, then reply" % w, "task": "t-" + w})
    replies, deadline = {}, time.time() + WAIT_S
    while len(replies) < len(workers) and time.time() < deadline:
        for m in recv():
            replies[m["from"]] = m["body"]
        time.sleep(0.2)
    with open(os.path.join(WS, "TEAM_RESULT.md"), "w") as f:
        f.write("# Team result\n\n")
        for w in workers:
            f.write("- %s: %s\n" % (w, replies.get(w, "no reply")))
    print("team lead: %d of %d workers replied" % (len(replies), len(workers)))
    return 0 if len(replies) == len(workers) else 1


def worker():
    deadline = time.time() + WAIT_S
    while time.time() < deadline:
        for m in recv():
            with open(os.path.join(WS, ME + ".txt"), "w") as f:
                f.write("%s did %s\n" % (ME, m.get("fields", {}).get("task", "its task")))
            post("/send", {"from": ME, "to": m["from"], "body": "done: wrote %s.txt" % ME})
            print("team worker %s: done" % ME)
            return 0
        time.sleep(0.2)
    print("team worker %s: no task arrived" % ME)
    return 1


if __name__ == "__main__":
    if not URL or not ME:
        print("not a team run: HEIMDALL_MAILBOX_URL or HEIMDALL_AGENT_ID is unset", file=sys.stderr)
        sys.exit(2)
    sys.exit(lead() if ROLE == "lead" else worker())
