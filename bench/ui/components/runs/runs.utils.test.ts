import type { BenchEvent, GridRow, Value } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { INCOMPLETE_REASON, gatewaySummary, outcomeTone, pairsOfRow, stripMetricKinds, sumValues } from "./runs.utils";

const obs = (v: number): Value => ({ state: "observed", v, evidence: "gateway" });

function event(seq: number, partial: Partial<BenchEvent>): BenchEvent {
	return {
		event_id: `r:gateway:${seq}`,
		run_id: "r",
		slot_id: "s",
		pair_id: "p",
		seq,
		turn_index: 0,
		ts: "2026-11-14T00:00:00Z",
		type: "model_request",
		source: "gateway",
		evidence: "gateway",
		...partial,
	};
}

describe("sumValues", () => {
	const cases: { name: string; values: (Value | undefined)[]; want: Value }[] = [
		{ name: "all observed", values: [obs(1), obs(2.5)], want: { state: "observed", v: 3.5, evidence: "gateway" } },
		{ name: "empty", values: [], want: { state: "unknown", v: null, reason: "none" } },
		{
			name: "unknown with bounds",
			values: [obs(1), { state: "unknown", v: null, bounds: [0.5, 2], reason: "telemetry_incomplete" }],
			want: { state: "unknown", v: null, evidence: "gateway", reason: "telemetry_incomplete", bounds: [1.5, 3] },
		},
		{
			name: "unknown without bounds",
			values: [obs(1), undefined],
			want: { state: "unknown", v: null, evidence: "gateway", reason: "partially_observed" },
		},
		{ name: "all not applicable", values: [{ state: "not_applicable", v: null }], want: { state: "not_applicable", v: null } },
	];
	for (const c of cases) {
		it(c.name, () => expect(sumValues(c.values, "none")).toEqual(c.want));
	}
});

describe("gatewaySummary", () => {
	const events = [
		event(1, { type: "model_request", model_requested: "m1", model_served: "m1", attempt_no: 1 }),
		event(2, { type: "model_request", model_requested: "m1", model_served: "m1", attempt_no: 2 }),
		event(3, {
			type: "model_response",
			model_requested: "m1",
			model_served: "m2",
			tokens: { fresh: 10, cache_read: 20, cache_write: 5, out: 1 },
			usd: obs(0.01),
		}),
		event(4, { type: "tool_call", target: "pytest" }),
		event(1, { event_id: "r:agent_stream:1", source: "agent_stream", type: "model_response" }),
	];

	it("counts requests, retries and fallbacks from gateway rows only", () => {
		const s = gatewaySummary(events, true);
		expect(s.calls.map((e) => e.event_id)).toEqual(["r:gateway:1", "r:gateway:2", "r:gateway:3"]);
		expect(s.requests).toBe(2);
		expect(s.retries).toBe(1);
		expect(s.fallbacks).toBe(1);
		expect(s.tokens).toEqual({ state: "observed", v: 36, evidence: "gateway" });
		expect(s.cost).toEqual({ state: "observed", v: 0.01, evidence: "gateway" });
	});

	it("prefers explicit retry and fallback events", () => {
		const s = gatewaySummary([...events, event(5, { type: "retry" }), event(6, { type: "retry" }), event(7, { type: "fallback" })], true);
		expect(s.retries).toBe(2);
		expect(s.fallbacks).toBe(1);
	});

	it("makes totals unknown when the gateway record is incomplete", () => {
		const s = gatewaySummary(events, false);
		expect(s.tokens.state).toBe("unknown");
		expect(s.tokens.reason).toBe(INCOMPLETE_REASON);
		expect(s.cost.state).toBe("unknown");
		expect(s.calls).toHaveLength(3);
	});

	it("never reports zero tokens for a run with no rows", () => {
		const s = gatewaySummary([], true);
		expect(s.tokens.state).toBe("unknown");
		expect(s.cost.state).toBe("unknown");
	});
});

describe("pairsOfRow", () => {
	it("groups cells by trial, in trial order", () => {
		const row: GridRow = {
			task: "t01",
			condition: "clean",
			discordant: true,
			diff: { task: "t01", mean_a: 0, mean_b: 1, d: 1, n_a: 2, n_b: 2, unknown_a: 0, unknown_b: 0 },
			cells: [
				{ slot_id: "s3", pair_id: "p2", run_id: null, arm: "A", trial: 2, outcome: null, y: { state: "unknown", v: null } },
				{ slot_id: "s1", pair_id: "p1", run_id: "r1", arm: "A", trial: 1, outcome: "fail", y: obs(0) },
				{ slot_id: "s2", pair_id: "p1", run_id: "r2", arm: "B", trial: 1, outcome: "pass", y: obs(1) },
				{ slot_id: "s4", pair_id: "p2", run_id: null, arm: "B", trial: 2, outcome: null, y: { state: "unknown", v: null } },
			],
		};
		expect(pairsOfRow(row)).toEqual([
			{ pairId: "p1", trial: 1, outcomes: { A: "fail", B: "pass" }, hasRuns: true },
			{ pairId: "p2", trial: 2, outcomes: { A: null, B: null }, hasRuns: false },
		]);
	});
});

describe("outcomeTone", () => {
	it("scores outcomes as the verifier does", () => {
		expect(outcomeTone("pass")).toBe("pass");
		expect(outcomeTone("timeout")).toBe("zero");
		expect(outcomeTone("integrity_violation")).toBe("zero");
		expect(outcomeTone("infra_error")).toBe("unknown");
		expect(outcomeTone(null)).toBe("unknown");
	});
});

describe("stripMetricKinds", () => {
	it("takes the catalog's kind and falls back to the default", () => {
		const kinds = stripMetricKinds([
			{
				name: "steps",
				version: "1",
				label: "Steps",
				kind: "tokens",
				higher_is_better: false,
				min_evidence: "gateway",
				description: "",
				ps2: true,
			},
		]);
		expect(kinds.steps).toBe("tokens");
		expect(kinds.cost_usd).toBe("usd");
	});
});