import "@/lib/i18n";

import type { BenchEvent, RunSteps, StepRecord } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RunExportMenu } from "./runExportMenu";
import { TraceView } from "./traceView";
import { filterCounts, filterTrace, formatDuration, runTraceUrl, stepIO, tokenizeJson, traceKind, usageByRequest } from "./traceView.utils";

const step = (over: Partial<StepRecord>): StepRecord => ({
	seq: 1,
	ts: "2026-11-15T10:00:01Z",
	kind: "model_turn",
	source: "gateway",
	proof: "verified",
	turn: 1,
	summary: "",
	...over,
});

const steps: StepRecord[] = [
	step({ seq: 1, summary: "model answered", request_id: "q1" }),
	step({
		seq: 2,
		kind: "tool_request",
		tool_name: "bash",
		summary: "<script>alert(1)</script>",
	}),
	step({
		seq: 3,
		kind: "command",
		source: "process_observer",
		proof: "strong",
		summary: "pytest -q",
		exit_code: 1,
		duration_ms: 4200,
	}),
	step({
		seq: 4,
		kind: "file_change",
		source: "workspace_observer",
		path: "src/a.py",
		summary: "edit src/a.py",
	}),
];

const data: RunSteps = { run_id: "r1", available: true, steps, observers: [] };

describe("traceView.utils", () => {
	it("classifies, filters and searches", () => {
		expect(steps.map(traceKind)).toEqual(["model", "tool", "command", "file"]);
		expect(filterCounts(steps)).toEqual({
			all: 4,
			model: 1,
			tools: 1,
			commands: 1,
			errors: 1,
			mismatches: 0,
		});
		expect(filterTrace(steps, "errors", "").map((s) => s.seq)).toEqual([3]);
		expect(filterTrace(steps, "all", "PYTEST").map((s) => s.seq)).toEqual([3]);
		expect(filterTrace(steps, "model", "pytest")).toEqual([]);
	});
	it("sums tokens and cost per request", () => {
		const events = [
			{
				request_id: "q1",
				tokens: { fresh: 10, cache_read: 5, cache_write: 0, out: 20 },
				usd: { state: "observed", v: 0.002 },
			},
			{
				request_id: "q1",
				tokens: { fresh: 1, cache_read: 0, cache_write: 0, out: 1 },
			},
			{ tool_name: "bash" },
		] as unknown as BenchEvent[];
		expect(usageByRequest(events).get("q1")).toEqual({
			tokens: 37,
			usd: 0.002,
		});
	});
	it("splits a step into input and output and tokenizes JSON", () => {
		const io = stepIO(steps[2]);
		expect(io.input).toMatchObject({ kind: "command", summary: "pytest -q" });
		expect(io.output).toMatchObject({
			exit_code: 1,
			duration_ms: 4200,
			proof: "strong",
		});
		const toks = tokenizeJson({ a: 1, b: "x", c: null });
		expect(toks.map((t) => t.text).join("")).toBe(JSON.stringify({ a: 1, b: "x", c: null }, null, 2));
		expect(toks.find((t) => t.text === '"a"')?.kind).toBe("key");
		expect(toks.find((t) => t.text === "1")?.kind).toBe("number");
	});
	it("formats durations and urls", () => {
		expect(formatDuration(820)).toBe("820 ms");
		expect(formatDuration(4200)).toBe("4.2 s");
		expect(formatDuration(185000)).toBe("3m 05s");
		expect(runTraceUrl("/api/bench", "r 1", "otlp")).toBe("/api/bench/runs/r%201/trace?format=otlp");
	});
});

describe("TraceView", () => {
	it("renders one row per step with filters, inert text and chips", () => {
		const events = [
			{
				request_id: "q1",
				tokens: { fresh: 100, cache_read: 0, cache_write: 0, out: 20 },
			},
		] as unknown as BenchEvent[];
		const html = renderToStaticMarkup(<TraceView data={data} events={events} arm="A" />);
		for (const seq of [1, 2, 3, 4]) expect(html).toContain(`data-testid="trace-row-A-${seq}"`);
		expect(html).toContain('data-testid="trace-filter-errors"');
		expect(html).toContain('data-testid="trace-search"');
		expect(html).toContain('data-testid="trace-row-A-1-tokens"');
		expect(html).toContain("4.2 s");
		expect(html).not.toContain("<script>");
		expect(html).not.toContain('data-testid="trace-live"');
	});
	it("shows the Live pulse and waits for steps on a live run", () => {
		const html = renderToStaticMarkup(<TraceView data={{ ...data, available: false, steps: [] }} arm="A" live />);
		expect(html).toContain('data-testid="trace-live"');
		expect(html).toContain('data-testid="trace-autoscroll"');
		expect(html).toContain('data-testid="trace-A-empty"');
	});
	it("filters at first render", () => {
		const html = renderToStaticMarkup(<TraceView data={data} arm="A" initialFilter="commands" />);
		expect(html).toContain('data-testid="trace-row-A-3"');
		expect(html).not.toContain('data-testid="trace-row-A-1"');
	});
});

describe("RunExportMenu", () => {
	it("renders the Export button closed", () => {
		const html = renderToStaticMarkup(<RunExportMenu runId="r1" sealed />);
		expect(html).toContain('data-testid="run-export-button"');
		expect(html).toContain("Export");
	});
});

describe("TraceView initial step (N51)", () => {
	it("selects the step a link asked for, with every step shown", () => {
		const html = renderToStaticMarkup(<TraceView data={data} arm="A" initialFilter="commands" initialStep={2} />);
		expect(html).toMatch(/data-selected="true"[^>]*data-testid="trace-row-A-2"|data-testid="trace-row-A-2"[^>]*data-selected="true"/);
		expect(html).toContain('data-testid="trace-row-A-1"');
	});
	it("ignores a step that is not in the trace", () => {
		const html = renderToStaticMarkup(<TraceView data={data} arm="A" initialStep={99} />);
		expect(html).not.toContain('data-selected="true"');
	});
});