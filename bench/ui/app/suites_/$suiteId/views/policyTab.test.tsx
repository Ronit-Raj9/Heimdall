import "@/lib/i18n";

import type { PolicyDraft, PolicyReport } from "@/lib/types/policyDocs";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { generateParams, pendingByClause, rateText, ReportView } from "./policyTab";

const draft = (task: string, clause: string, status = "pending"): PolicyDraft => ({
	task,
	clause,
	kind: "adversarial",
	title: task,
	opening: "",
	check: "",
	violation: "",
	status,
	model: "m",
	cost_usd: 0,
	at: "",
});

describe("policy tab", () => {
	it("asks for an estimate or a confirmed run within the budget", () => {
		const v = { suite: "desk", doc: "refunds", provider: " openai ", model: "gpt-x", budget: "2" };
		expect(generateParams(v, true)).toEqual({ suite: "desk", doc: "refunds", provider: "openai", model: "gpt-x", budget_usd: 2, estimate: true });
		expect(generateParams({ ...v, budget: "" }, false)).toEqual({ suite: "desk", doc: "refunds", provider: "openai", model: "gpt-x", yes: true });
	});
	it("groups pending drafts by clause", () => {
		const g = pendingByClause([draft("a", "1"), draft("b", "2"), draft("c", "1"), draft("d", "1", "accepted")]);
		expect(g.map(([c, ds]) => [c, ds.map((d) => d.task)])).toEqual([
			["1", ["a", "c"]],
			["2", ["b"]],
		]);
	});
	it("shows per-clause pass rates with ranges, findings and downloads", () => {
		const rate = { runs: 3, passed: 2, rate: 2 / 3, lo: 0.21, hi: 0.94 };
		expect(rateText(rate)).toBe("67% (21% to 94%)");
		const report: PolicyReport = {
			suite: "desk",
			doc: "refunds",
			title: "Refunds",
			clauses: 2,
			tested_clauses: 1,
			accepted_tasks: 3,
			pending_tasks: 0,
			runs: 3,
			agents: [
				{
					agent: "bot v2",
					total: rate,
					by_kind: {},
					clauses: [{ number: "4", title: "No refunds after 30 days", total: rate, by_kind: {} }],
					findings: [{ clause: "4", kind: "insists", text: "Breaks clause 4 when the customer insists.", examples: [{ run_id: "r1", exp_id: "e1", task: "t" }] }],
				},
			],
			untested_clauses: ["5"],
			notes: [],
		};
		const html = renderToStaticMarkup(<ReportView suite="desk" report={report} />);
		expect(html).toContain("1 of 2 clauses tested, 3 runs.");
		expect(html).toContain("No refunds after 30 days");
		expect(html).toContain("Breaks clause 4 when the customer insists.");
		expect(html).toContain("/api/bench/suites/desk/policy-docs/refunds/report?format=pdf");
		expect(html).toContain("Untested clauses: 5");
	});
});
