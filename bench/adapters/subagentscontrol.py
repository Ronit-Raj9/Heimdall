# Scripted in-process multi-agent control (docs/multi-agent.md): one
# container whose "agents" are threads of this script. It reports them as
# native agent events on stdout: a planner subagent starts two workers that
# run at the same time, each writes one file (a tool_call event naming its
# path), the planner hands off to a reviewer, and every subagent ends.
# Deterministic, no model calls.
import json, os, threading, time

WS = os.environ.get("BENCH_WORKSPACE", "/workspace")
lock = threading.Lock()


def ms():
    return int(time.time() * 1000)


def event(**e):
    with lock:
        print("@@heimdall-event " + json.dumps(e, sort_keys=True), flush=True)


def start(eid, name, role, parent=""):
    event(type="subagent_start", id=eid, parent_id=parent, name=name, start_time=ms(),
          attributes={"agent_id": name, "agent_role": role})


def end(eid, name, parent):
    event(type="subagent_end", id=eid + "-end", parent_id=parent, name=name, start_time=ms(), status="ok",
          attributes={"agent_id": name})


def work(name):
    start("s-" + name, name, "worker", "s-planner")
    time.sleep(0.4)
    path = name + ".txt"
    t0 = ms()
    with open(os.path.join(WS, path), "w") as f:
        f.write("%s wrote this\n" % name)
    event(type="tool_call", id="t-" + name, parent_id="s-" + name, name="write_file", input={"path": path},
          output="ok", start_time=t0, end_time=ms(), status="ok", attributes={"agent_id": name})
    time.sleep(0.4)
    end("s-" + name, name, "s-planner")


if __name__ == "__main__":
    start("s-planner", "planner", "lead")
    event(type="plan", id="p1", parent_id="s-planner", output="split the work between worker-a and worker-b",
          start_time=ms(), attributes={"agent_id": "planner"})
    threads = [threading.Thread(target=work, args=(n,)) for n in ("worker-a", "worker-b")]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    event(type="handoff", id="h1", parent_id="s-planner", name="handoff to reviewer", start_time=ms(),
          attributes={"from_agent": "planner", "to_agent": "reviewer"})
    start("s-reviewer", "reviewer", "reviewer", "s-planner")
    with open(os.path.join(WS, "REVIEW.md"), "w") as f:
        f.write("reviewed worker-a.txt and worker-b.txt\n")
    end("s-reviewer", "reviewer", "s-planner")
    end("s-planner", "planner", "")
    print("subagents control: done")
