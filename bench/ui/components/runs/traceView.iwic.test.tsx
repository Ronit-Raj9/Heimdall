import "@/lib/i18n";

import type { RunSteps } from "@/lib/types/bench";
import { tagOf } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TraceView } from "./traceView";

// Wave 4, N28: the tools chip counts tool uses only, so its hint says the list shows each use with its answer.

const data: RunSteps = {
	run_id: "r1",
	available: true,
	observers: [],
	steps: [
		{
			seq: 1,
			ts: "2026-11-15T10:00:01Z",
			kind: "tool_request",
			source: "gateway",
			proof: "verified",
			turn: 1,
			summary: "",
			tool_name: "bash",
		},
	],
};

describe("TraceView tools chip (N28)", () => {
	it("explains that each tool use and its answer are listed", () => {
		const html = renderToStaticMarkup(<TraceView data={data} arm="A" />);
		expect(tagOf(html, "trace-filter-tools")).toContain('title="Each tool use and its answer are listed"');
		expect(tagOf(html, "trace-filter-model")).not.toContain("title=");
	});
});