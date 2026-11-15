// Agent events: report an agent's own steps to the Heimdall bench, TypeScript edition.
//
//   BENCH_EVENTS_FILE=/tmp/events.jsonl node --experimental-strip-types agent-events.ts && cat /tmp/events.jsonl
//
// Inside a bench run the bench sets BENCH_EVENTS_URL or BENCH_EVENTS_FILE in
// the agent container; with neither set every call below is a no-op, so the
// same code runs anywhere. The bench shows these events as agent-reported
// evidence, next to what it observed itself. spec/agent-event.schema.json is
// the format.
import { emit, flushEvents, stdoutLine, step } from "../typescript/src/index.ts";

// The three lines an agent adds: a plan, then each tool call with its result.
const plan = emit("plan", "fix the failing test", { output: "1. run the tests 2. read the failure 3. patch" });
const call = emit("tool_call", "run_shell", { parentId: plan, toolCallId: "call_1", input: { command: "npm test" } });
emit("tool_result", "run_shell", { parentId: call, toolCallId: "call_1", output: "1 failed", status: "error" });

// A subagent nests under its start event; step() times a function and reports it when it settles.
let subId = "";
await step(
	"subagent_start",
	"reviewer",
	async (sub) => {
		subId = sub.id;
		await step(
			"retrieval",
			"search_docs",
			(hit) => {
				hit.output = ["docs/testing.md"];
			},
			{ parentId: sub.id, input: { query: "assertion error" } },
		);
		sub.output = "the fixture is stale";
	},
	{ parentId: plan },
);
emit("subagent_end", "reviewer", { parentId: subId, status: "ok" });

// An agent that can only print writes the same event to stdout instead.
console.log(stdoutLine("message", "final", { parentId: plan, output: "patched the fixture" }));

// Delivery runs in the background: wait for it before the process exits.
await flushEvents();
