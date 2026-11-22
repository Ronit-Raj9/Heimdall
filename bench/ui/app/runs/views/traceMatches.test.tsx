import "@/lib/i18n";

import type { SearchResponse } from "@/lib/types/traceTools";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { TraceMatchList } from "./traceMatches";
import { benchmarkParam, hitAgentLabel, splitHighlights } from "./traceMatches.utils";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("splitHighlights", () => {
	it("marks spans by code point, not UTF-16 unit", () => {
		// The emoji is two UTF-16 units but one code point.
		expect(splitHighlights("a\u{1F600}refund ok", [[2, 8]])).toEqual([
			{ text: "a\u{1F600}", mark: false },
			{ text: "refund", mark: true },
			{ text: " ok", mark: false },
		]);
	});
	it("merges overlaps, clamps out-of-range and drops empty spans", () => {
		expect(
			splitHighlights("abcdef", [
				[4, 99],
				[0, 2],
				[1, 3],
				[5, 5],
			]),
		).toEqual([
			{ text: "abc", mark: true },
			{ text: "d", mark: false },
			{ text: "ef", mark: true },
		]);
		expect(splitHighlights("abc", [])).toEqual([{ text: "abc", mark: false }]);
		expect(splitHighlights("", undefined)).toEqual([{ text: "", mark: false }]);
	});
	it("handles a span covering everything", () => {
		expect(splitHighlights("pass", [[0, 4]])).toEqual([{ text: "pass", mark: true }]);
	});
});

describe("trace search params", () => {
	it("drops the version from the benchmark filter", () => {
		expect(benchmarkParam("support@1.2")).toBe("support");
		expect(benchmarkParam(null)).toBeUndefined();
		expect(benchmarkParam("")).toBeUndefined();
	});
	it("labels agents with their version", () => {
		expect(hitAgentLabel({ agent: "bot" })).toBe("bot");
		expect(hitAgentLabel({ agent: "bot", version: "3" })).toBe("bot v3");
		expect(hitAgentLabel({ agent: "bot", version: "v3" })).toBe("bot v3");
	});
});

const data: SearchResponse = {
	query: "refund",
	total: 120,
	truncated: true,
	indexed_runs: 400,
	hits: [
		{
			run_id: "r1",
			experiment: "e1",
			benchmark: "support",
			task: "refund-late-order",
			agent: "bot",
			version: "2",
			outcome: "pass",
			score: 1,
			matches: [
				{ field: "tool_call", seq: 4, label: "refund_order", text: "refund_order(id=7)", highlights: [[0, 6]] },
				{ field: "answer", text: "Refund sent", highlights: [[0, 6]] },
				{ field: "step", text: "a", highlights: [] },
				{ field: "grade", text: "b", highlights: [] },
			],
		},
	],
};

describe("TraceMatchList", () => {
	it("shows totals, truncation, labels and highlighted snippets", () => {
		const html = renderToStaticMarkup(<TraceMatchList data={data} onOpen={() => {}} />);
		expect(html).toContain("Trace matches");
		expect(html).toContain("120 runs match");
		expect(html).toContain("showing first 1");
		expect(html).toContain("bot v2");
		expect(html).toContain("Tool call");
		expect(html).toContain("<mark");
		expect(html).toContain("1 more match");
		expect(html.match(/data-testid="trace-match"/g)?.length).toBe(3);
	});
	it("has an empty state", () => {
		const html = renderToStaticMarkup(<TraceMatchList data={{ ...data, total: 0, truncated: false, hits: [] }} onOpen={() => {}} />);
		expect(html).toContain("No trace matches");
	});
});

describe("trace search fields (N49)", () => {
	it("labels model error and event matches", () => {
		const hit = data.hits[0];
		const withErrors: SearchResponse = {
			...data,
			hits: [
				{
					...hit,
					matches: [
						{ field: "error", label: "gpt-6-luna", text: "The server is overloaded", highlights: [[15, 25]] },
						{ field: "event", text: "compaction", highlights: [] },
					],
				},
			],
		};
		const html = renderToStaticMarkup(<TraceMatchList data={withErrors} onOpen={() => {}} />);
		expect(html).toContain(">Error<");
		expect(html).toContain(">Event<");
	});
});