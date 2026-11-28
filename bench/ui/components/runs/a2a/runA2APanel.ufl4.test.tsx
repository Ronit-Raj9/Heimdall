import "@/lib/i18n";

import { runA2APath } from "@/lib/store/apis/runA2AApi";
import type { RunA2AResponse } from "@/lib/types/runA2A";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { A2AWitness } from "./runA2APanel";

// The body GET /runs/{id}/a2a serves for the fixture of handlers/runa2a_test.go, plus a linked row.
const body: RunA2AResponse = JSON.parse(
	`{"run_id":"r_t01_A","available":true,"a2a":{"entry":"hb-r-1-lead","complete":true,"strength":"witnessed",` +
		`"operations":[{"id":"op1","timestamp":"2026-11-18T08:00:00Z","operation":"SendMessage","status":"success","agent":"hb-r-1-lead","member":"lead","caller":"bench","task_id":"t1"}],` +
		`"hops":[{"from":"lead","to":"writer","operation_id":"op2","source":"gateway_key"}],` +
		`"linked":[{"kind":"llm","id":"l1","task_id":"t1","member":"writer","on_run_key":true}],` +
		`"agreement":{"task_id":"t1","launcher_state":"TASK_STATE_COMPLETED","gateway_state":"TASK_STATE_COMPLETED","answer_matches":true}}}`,
);

describe("A2A run witness", () => {
	it("requests the run's own a2a route", () => {
		expect(runA2APath("r/1")).toBe("/runs/r%2F1/a2a");
	});
	it("shows hops, linked rows and answer agreement", () => {
		const html = renderToStaticMarkup(<A2AWitness a2a={body.a2a!} />);
		expect(html).toContain("Agent hops");
		expect(html).toContain("Witnessed");
		expect(html).toContain("lead to writer");
		expect(html).toContain("LLM l1");
		expect(html).toContain("on run key");
		expect(html).toContain("Matches the gateway");
	});
	it("says when no agent called another", () => {
		const html = renderToStaticMarkup(<A2AWitness a2a={{ entry: "e", complete: false, strength: "partial", reason: "log lagging" }} />);
		expect(html).toContain("No agent called another agent.");
		expect(html).toContain("Partly witnessed");
		expect(html).not.toContain('data-testid="run-a2a-agreement"');
	});
});
