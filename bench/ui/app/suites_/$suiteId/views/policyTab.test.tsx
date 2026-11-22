import "@/lib/i18n";

import { benchApi } from "@/lib/store/apis/benchApi";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import type { SuiteSummary } from "@/lib/types/bench";
import type { PolicyDocView, PolicyDraft, PolicyReport } from "@/lib/types/policyDocs";
import { configureStore } from "@reduxjs/toolkit";
import { renderToStaticMarkup } from "react-dom/server";
import { Provider } from "react-redux";
import { describe, expect, it, vi } from "vitest";
import { ClauseEditor, generateParams, pendingByClause, PolicyTab, rateText, ReportView, updateClause } from "./policyTab";

const pol = vi.hoisted(() => ({ list: {} as unknown, view: {} as unknown }));
vi.mock("@/lib/store/apis/policyDocsApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/policyDocsApi")>()),
	useListPolicyDocsQuery: () => pol.list,
	useGetPolicyDocQuery: () => pol.view,
	useGetPolicyReportQuery: () => ({ data: undefined }),
	useDeletePolicyDocMutation: () => mutation(),
	useUploadPolicyDocMutation: () => mutation(),
	useReviewPolicyDraftsMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/apis/platformApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/platformApi")>()),
	useGetJobQuery: () => ({ data: undefined }),
}));

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
		expect(generateParams(v, true)).toEqual({
			suite: "desk",
			doc: "refunds",
			provider: "openai",
			model: "gpt-x",
			budget_usd: 2,
			estimate: true,
		});
		expect(generateParams({ ...v, budget: "" }, false)).toEqual({
			suite: "desk",
			doc: "refunds",
			provider: "openai",
			model: "gpt-x",
			yes: true,
		});
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
					findings: [
						{
							clause: "4",
							kind: "insists",
							text: "Breaks clause 4 when the customer insists.",
							examples: [{ run_id: "r1", exp_id: "e1", task: "t" }],
						},
					],
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
	it("edits one clause and renders the clause editor", () => {
		const cs = [
			{ number: "1", text: "Refund within 30 days." },
			{ number: "2", text: "No store credit.", title: "Credit" },
		];
		const next = updateClause(cs, 1, { skip: true, text: "No credit." });
		expect(next[1]).toEqual({ number: "2", text: "No credit.", title: "Credit", skip: true });
		expect(next[0]).toBe(cs[0]);
		const store = configureStore({
			reducer: { [benchApi.reducerPath]: benchApi.reducer },
			middleware: (g) => g().concat(benchApi.middleware),
		});
		const view = {
			suite: "desk",
			doc: { id: "refunds", clauses: cs, drafts: [] },
			counts: {},
			grader: "checks",
			judged: true,
			next: "",
		} as unknown as PolicyDocView;
		const html = renderToStaticMarkup(
			<Provider store={store}>
				<ClauseEditor suite="desk" view={view} onClose={() => undefined} />
			</Provider>,
		);
		expect(html).toContain('data-testid="policy-clause-editor"');
		expect(html).toContain('data-testid="policy-clause-edit-1"');
		expect(html).toContain("No store credit.");
		expect(html).toContain("Save clauses");
	});
	it("explains up front that a benchmark without a support task cannot hold policy tasks, and disables upload, estimate and draft", () => {
		const docView = {
			suite: "zz",
			doc: { id: "refunds", title: "Refunds", clauses: [{ number: "1", text: "x" }], drafts: [] },
			counts: {},
			grader: "checks",
			judged: true,
			next: "",
		};
		pol.view = okQuery(docView);
		const suite = { id: "zz-test-rows" } as unknown as SuiteSummary;
		const noShape = {
			suite: "zz-test-rows",
			docs: [{ id: "refunds", title: "Refunds", uploaded_at: "" }],
			next: "no task of this benchmark has a simulated customer",
		};
		pol.list = okQuery(noShape);
		let html = render(<PolicyTab suite={suite} />, hostedSession("member"));
		expect(html).toContain('data-testid="policy-no-shape"');
		expect(html).toContain(
			"Policy tasks are support conversations: this benchmark has no task with a simulated customer. Use a support benchmark (Benchmarks &gt; New, template support).",
		);
		for (const id of ["policy-upload-file", "policy-generate-estimate", "policy-generate-start", "policy-generate-provider"]) {
			expect(tagOf(html, id) ?? "", id).toContain(' disabled=""');
		}
		pol.list = okQuery({ ...noShape, next: undefined, shape: "refund-in-policy" });
		html = render(<PolicyTab suite={suite} />, hostedSession("member"));
		expect(html).not.toContain('data-testid="policy-no-shape"');
		expect(tagOf(html, "policy-upload-file") ?? "").not.toContain(' disabled=""');
	});
});