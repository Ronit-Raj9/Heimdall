import "@/lib/i18n";

import { errorQuery, loadingQuery, mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { RunDetail, StepRecord } from "@/lib/types/bench";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RunDebugActions, canReplay } from "./debugActions";
import { DebugBanner } from "./debugBanner";
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
	({ seq: 1, ts: "2026-11-22T00:00:00Z", kind: "model_turn", source: "gateway", proof: "verified", turn: 1, summary: "", ...over }) as StepRecord;

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
		const html = render(<StepInspector runId="r_1" step={step({ request_id: "q1" })} call={{ call: 1, status: 200, action: "pass" }} canFork={false} onClose={() => undefined} />);
		expect(html).not.toContain("inspector-fork");
		expect(html).toContain("inspector-raw-loading");
		api.call = errorQuery(404);
		expect(render(<StepInspector runId="r_1" step={step({ request_id: "q1" })} canFork onClose={() => undefined} />)).toContain("inspector-raw-error");
		api.call = okQuery({ run_id: "r_1", request_id: "q1", status: 200, request: "", response: "", available: false });
		expect(render(<StepInspector runId="r_1" step={step({ request_id: "q1" })} canFork onClose={() => undefined} />)).toContain("inspector-raw-missing");
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
