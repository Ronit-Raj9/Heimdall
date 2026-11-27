import "@/lib/i18n";

import type { RunListRow } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { RunTable } from "./runList";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const row = (arm: string, extra: Partial<RunListRow> = {}): RunListRow => ({
	run_id: `r_${arm.toLowerCase()}1`,
	exp_id: "e_x",
	pair_id: "p_1",
	paired: true,
	suite: "coding-core",
	track: "coding",
	output: "diff",
	grading: "verified",
	task: "t01",
	arm,
	condition: "clean",
	trial: 1,
	attempt: 1,
	counted: true,
	sealed: true,
	outcome: "pass",
	y: { state: "observed", v: 1, evidence: "outcome" },
	config_id: `cfg-${arm}`,
	started: "2026-11-14T08:00:00Z",
	...extra,
});

// Lane 4a phase B (M4-21): the Score column shows only with a score other than 0 or 1, so the fixtures carry one.
describe("RunTable", () => {
	it("lists every arm, links every id, and opens the pair in the comparison", () => {
		const html = renderToStaticMarkup(
			<RunTable rows={[row("A"), row("B", { y: { state: "observed", v: 0.5 } }), row("C", { outcome: "fail", sealed: true })]} />,
		);
		for (const arm of ["A", "B", "C"]) expect(html).toContain(`data-arm="${arm}"`);
		expect(html).toContain('href="/runs/r_c1"');
		expect(html).toContain('href="/experiments/e_x"');
		expect(html).toContain('href="/suites/coding-core?tab=tasks&amp;task=t01"');
		expect(html).toContain('href="/compare?pair=p_1&amp;exp=e_x"');
		expect(html).toContain('data-grade-source="verified"');
	});
	it("offers no comparison for a run of a one-setup or debug experiment (N27)", () => {
		const html = renderToStaticMarkup(<RunTable rows={[row("A", { paired: false }), row("B", { paired: undefined })]} />);
		expect(html).not.toContain("/compare?");
		expect(html).not.toContain("-compare");
	});
	it("marks a judged score and a run still in flight", () => {
		const html = renderToStaticMarkup(
			<RunTable
				rows={[row("A", { grading: "judged", y: { state: "observed", v: 0.5 } }), row("B", { sealed: false })]}
				showExperiment={false}
			/>,
		);
		expect(html).toContain('data-testid="run-row-r_a1-judged"');
		expect(html).toContain("Running");
		expect(html).not.toContain('href="/experiments/e_x"');
	});
});