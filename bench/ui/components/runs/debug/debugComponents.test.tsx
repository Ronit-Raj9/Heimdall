import "@/lib/i18n";

import { errorQuery, loadingQuery, mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { RunDetail, StepRecord } from "@/lib/types/bench";
import type { DebugRunResponse } from "@/lib/types/runDebug";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/lib/i18n";
import { RunDebugActions, canBreak, canReplay } from "./debugActions";
import { DebugBanner } from "./debugBanner";
import { startDebugRun } from "./debugDialogs";
import { HoldCard, HoldPanel } from "./holdPanel";
import { StepInspector } from "./stepInspector";
import { DiffTable } from "./traceDiff";

const api = vi.hoisted(() => ({ holds: {} as unknown, call: {} as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/runDebugApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/runDebugApi")>()),
	useGetRunHoldsQuery: () => api.holds,
	useGetRunCallQuery: () => api.call,
	useStartDebugRunMutation: () => mutation(),
	useReleaseHoldMutation: () => mutation(),
}));

const hold = {
	slot: "fp-slot-1",
	run_id: "r_1",
	call: 3,
	since: "2026-11-22T00:00:00Z",
	deadline: "2099-01-01T00:00:00Z",
	reason: "call 3",
	request: '{"messages":[]}',
};

const step = (over: Partial<StepRecord>): StepRecord =>
	({
		seq: 1,
		ts: "2026-11-22T00:00:00Z",
		kind: "model_turn",
		source: "gateway",
		proof: "verified",
		turn: 1,
		summary: "",
		...over,
	}) as StepRecord;

const detail = (over: { sealed?: boolean; condition?: string; debug?: RunDetail["run"]["debug"] } = {}) =>
	({
		sealed: over.sealed ?? true,
		slot: { exp_id: "e_1", condition: over.condition ?? "clean", arm: "A", task: "t1" },
		run: { run_id: "r_1", debug: over.debug },
	}) as unknown as RunDetail;

beforeEach(() => {
	api.holds = okQuery({ run_id: "r_1", holds: [], held_ms: 0 });
	api.call = okQuery({ run_id: "r_1", request_id: "q1", status: 200, request: '{"model":"m"}', response: '{"id":"msg"}', available: true });
});

describe("hold panel", () => {
	it("renders nothing while no call is held", () => {
		expect(render(<HoldPanel runId="r_1" />)).toBe("");
	});
	it("shows the held call with its controls", () => {
		api.holds = okQuery({ run_id: "r_1", holds: [hold], held_ms: 2000 });
		const html = render(<HoldPanel runId="r_1" />);
		expect(tagOf(html, "hold-panel")).toContain('data-call="3"');
		for (const id of ["hold-continue", "hold-step", "hold-edit", "hold-abort", "hold-countdown"]) expect(html).toContain(id);
		expect(html).toContain("Paused before model call 3");
		expect(html).toContain("F8 continue, F10 step");
	});
	it("says when the run was stopped", () => {
		api.holds = okQuery({ run_id: "r_1", holds: [], held_ms: 0, stopped: true });
		expect(render(<HoldPanel runId="r_1" />)).toContain("hold-stopped");
	});
	it("card renders the pending request as text", () => {
		const html = render(<HoldCard runId="r_1" hold={{ ...hold, request: "<script>x</script>" }} onDone={() => undefined} />);
		expect(html).not.toContain("<script>");
	});
});

describe("run debug actions", () => {
	it("offers replay only on a finished run without a fault", () => {
		expect(canReplay(detail())).toBe(true);
		expect(canReplay(detail({ sealed: false }))).toBe(false);
		expect(canReplay(detail({ debug: { mode: "replay", from_run: "r_0" } }))).toBe(false);
		const html = render(<RunDebugActions detail={detail()} />);
		expect(html).toContain("run-replay");
		expect(html).toContain("run-debug");
		expect(render(<RunDebugActions detail={detail({ sealed: false })} />)).not.toContain("run-replay");
		// N133: a fault run never hides the actions silently; debugActions.test.tsx covers the rule.
		expect(render(<RunDebugActions detail={detail({ condition: "p_outage" })} />)).toContain("run-debug-reason");
	});
});

describe("debug banner", () => {
	it("links the source run and says it never counts", () => {
		const html = render(<DebugBanner debug={{ mode: "fork", from_run: "r_0", call: 4, held_ms: 65000 }} />);
		expect(html).toContain("Fork of run r_0 from call 4");
		expect(html).toContain("never count");
		expect(html).toContain("1m 05s");
	});
});

describe("step inspector", () => {
	it("asks for a selection when empty", () => {
		expect(render(<StepInspector runId="r_1" step={null} canFork onClose={() => undefined} />)).toContain("step-inspector-empty");
	});
	it("shows the raw call and the fork entry point", () => {
		const html = render(
			<StepInspector
				runId="r_1"
				step={step({ request_id: "q1" })}
				call={{ call: 2, request_id: "q1", status: 200, action: "pass", replay: "divergence" }}
				canFork
				onClose={() => undefined}
			/>,
		);
		expect(html).toContain("inspector-request");
		expect(html).toContain("&quot;model&quot;:&quot;m&quot;");
		expect(html).toContain("inspector-fork");
		expect(html).toContain("inspector-breakpoint");
		expect(html).toContain('data-replay="divergence"');
	});
	it("hides fork when the run cannot be forked and reports load states", () => {
		api.call = loadingQuery();
		const html = render(
			<StepInspector
				runId="r_1"
				step={step({ request_id: "q1" })}
				call={{ call: 1, status: 200, action: "pass" }}
				canFork={false}
				onClose={() => undefined}
			/>,
		);
		expect(html).not.toContain("inspector-fork");
		expect(html).toContain("inspector-raw-loading");
		api.call = errorQuery(404);
		expect(render(<StepInspector runId="r_1" step={step({ request_id: "q1" })} canFork onClose={() => undefined} />)).toContain(
			"inspector-raw-error",
		);
		api.call = okQuery({ run_id: "r_1", request_id: "q1", status: 200, request: "", response: "", available: false });
		expect(render(<StepInspector runId="r_1" step={step({ request_id: "q1" })} canFork onClose={() => undefined} />)).toContain(
			"inspector-raw-missing",
		);
	});
});

describe("trace diff", () => {
	it("marks the first differing model call", () => {
		const left = [step({ seq: 1, turn: 1 }), step({ seq: 2, turn: 1, kind: "tool_request", tool_name: "bash" })];
		const right = [step({ seq: 1, turn: 1 }), step({ seq: 2, turn: 1, kind: "tool_request", tool_name: "edit" })];
		const html = render(<DiffTable left={left} right={right} />);
		expect(html).toContain("First difference at model call 1");
		expect(tagOf(html, "trace-diff-row-1")).toContain('data-same="false"');
		expect(render(<DiffTable left={left} right={left} />)).toContain("No difference");
	});
});
describe("debug on a server that cannot hold model calls (N212)", () => {
	const withAllowed = (allowed: RunDetail["debug_allowed"]) =>
		({
			sealed: true,
			slot: { exp_id: "e_1", condition: "clean", arm: "A", task: "t1" },
			run: { run_id: "r_1" },
			debug_allowed: allowed,
		}) as unknown as RunDetail;
	it("hides Debug and says why, keeps Replay", () => {
		const d = withAllowed({ replay: true, fork: true, breakpoints: false, breakpoints_reason: "x" });
		expect(canBreak(d)).toBe(false);
		const html = render(<RunDebugActions detail={d} />);
		expect(html).toContain("run-replay");
		expect(html).not.toContain('data-testid="run-debug"');
		// M5-14: on a finished run the note moves into the Replay tooltip; a live run shows it as a line.
		expect(tagOf(html, "run-debug-breakpoints-reason")).toBeNull();
		expect(tagOf(render(<RunDebugActions detail={{ ...d, sealed: false }} />), "run-debug-breakpoints-reason")).not.toBeNull();
		expect(html).toContain(
			"Pausing needs a worker this server controls. Use Replay or Fork.",
		);
	});
	it("offers Debug where breakpoints work, and on an older server that does not say", () => {
		for (const d of [withAllowed({ replay: true, fork: true, breakpoints: true }), withAllowed({ replay: true, fork: true })]) {
			expect(canBreak(d)).toBe(true);
			const html = render(<RunDebugActions detail={d} />);
			expect(html).toContain('data-testid="run-debug"');
			expect(html).not.toContain("run-debug-breakpoints-reason");
		}
	});
	it("the step inspector hides Pause here when breakpoints cannot work", () => {
		const html = render(
			<StepInspector
				runId="r_1"
				step={step({ request_id: "q1" })}
				call={{ call: 2, request_id: "q1", status: 200, action: "pass" }}
				canFork
				canBreak={false}
				onClose={() => undefined}
			/>,
		);
		expect(html).toContain("inspector-fork");
		expect(html).not.toContain("inspector-breakpoint");
	});
});

describe("fork and breakpoint dialogs (N214)", () => {
	const started: DebugRunResponse = { exp_id: "e_dbg", mode: "fork", from_run: "r_1", started: true };
	it("a successful start hands the run up and closes the dialog, so the next fork opens a fresh one", async () => {
		const onStarted = vi.fn();
		const onClose = vi.fn();
		const run = vi.fn(async () => ({ data: started }));
		expect(await startDebugRun(run, onStarted, onClose)).toBe(true);
		expect(onStarted).toHaveBeenCalledWith(started);
		expect(onClose).toHaveBeenCalledTimes(1);
		// A second fork right after: the same path runs again, nothing stays disabled.
		expect(await startDebugRun(run, onStarted, onClose)).toBe(true);
		expect(run).toHaveBeenCalledTimes(2);
		expect(onClose).toHaveBeenCalledTimes(2);
	});
	it("a refused start keeps the dialog open with its error", async () => {
		const onStarted = vi.fn();
		const onClose = vi.fn();
		expect(await startDebugRun(async () => ({ error: { status: 409 } }), onStarted, onClose)).toBe(false);
		expect(onStarted).not.toHaveBeenCalled();
		expect(onClose).not.toHaveBeenCalled();
	});
	it("the dialogs state what the start costs", () => {
		expect(i18n.t("fork.cost", { ns: "runDebug", call: 4 })).toBe(
			"Calls before 4 replay free. Later calls cost money.",
		);
		expect(i18n.t("debugDialog.cost", { ns: "runDebug" })).toContain("costs money.");
	});
});