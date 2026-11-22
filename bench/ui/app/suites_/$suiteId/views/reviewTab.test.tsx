import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { mutation, render } from "@/lib/testing/render";
import type { GeneratedTaskList, QualityReport } from "@/lib/types/authoring";
import type { SuiteSummary } from "@/lib/types/bench";
import i18n from "i18next";
import { describe, expect, it, vi } from "vitest";
import { assistParams, canDecide, QualityBody, ReviewTab } from "./reviewTab";

const generated: GeneratedTaskList = {
	suite: "support-faq",
	pending: 1,
	accepted: 1,
	rejected: 0,
	note: "",
	tasks: [
		{
			task: "refund-window",
			title: "Refund window",
			split: "public",
			review: "pending",
			cost_usd: 0.0123,
			generated: [{ field: "answer.rubric", op: "rubric", model: "openai/gpt-test" }],
		},
		{
			task: "sso-plan",
			split: "public",
			review: "accepted",
			cost_usd: 0.002,
			generated: [{ field: "controls.exploit", op: "exploit" }],
			decision: { decision: "accepted", by: "ana@example.com", at: "2026-11-22T10:00:00Z", note: "proof looked right", generated: "ab" },
		},
	],
};

const queue = vi.hoisted(() => ({ data: undefined as unknown }));
queue.data = generated;

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/apis/authoringApi", () => ({
	useGetGeneratedTasksQuery: () => ({ data: queue.data, isError: false }),
	useSetTaskReviewMutation: () => mutation(),
	useLazyGetSuiteQualityQuery: () => [vi.fn(), { isFetching: false }],
	useSplitHeldOutMutation: () => mutation(),
}));

const qa = { id: "support-faq", grader: "judge", output: "answer", task_count: 2, admitted_count: 1, tasks: [] } as unknown as SuiteSummary;
const code = { ...qa, id: "repo", grader: "hidden_tests", output: undefined } as unknown as SuiteSummary;
const text = (html: string) => html.replace(/<[^>]+>/g, " ");

describe("ReviewTab", () => {
	it("offers drafting, the review queue, quality checks and the held-out split", () => {
		const html = render(<ReviewTab suite={qa} />);
		for (const id of ["suite-review-draft", "suite-review-queue", "suite-review-quality", "suite-review-split"]) {
			expect(html).toContain(`data-testid="${id}"`);
		}
		expect(html).toMatch(/data-testid="suite-review-op-rubric"[^>]*checked=""/);
		expect(html).toContain('data-testid="suite-review-need-model"');
		expect(html).toContain("1 waiting, 1 accepted, 0 rejected");
		expect(html).toContain('data-testid="suite-review-accept-refund-window"');
		expect(html).toContain("Accepted by ana@example.com: proof looked right");
		expect(html).toContain("answer.rubric");
		expect(text(html)).not.toMatch(/\b(oracle|noop|probe|canary|sealed|suite|runner|lease)\b/i);
		expect(html).not.toMatch(/—|–/);
	});
	it("explains that drafting is for question-and-answer benchmarks", () => {
		const html = render(<ReviewTab suite={code} />);
		expect(html).toContain('data-testid="suite-review-draft-answer-only"');
	});
	it("hides actions from a viewer", () => {
		const html = render(<ReviewTab suite={qa} />, { ...LOCAL_SESSION, authMode: "hosted", signedIn: true, role: "viewer" });
		expect(html).not.toContain("suite-review-accept-");
		expect(html).not.toContain('data-testid="suite-review-split"');
	});
});

describe("reviewTab helpers", () => {
	it("builds the assist params", () => {
		expect(assistParams("s", ["rubric"], "", "m", 1, true)).toBeNull();
		expect(assistParams("s", [], "openai", "m", 1, true)).toBeNull();
		expect(assistParams("s", ["rubric"], " openai ", "gpt", 2, true)).toEqual({
			suite: "s",
			ops: ["rubric"],
			provider: "openai",
			model: "gpt",
			budget_usd: 2,
			estimate: true,
		});
		expect(assistParams("s", ["exploit"], "openai", "gpt", 0, false)).toMatchObject({ yes: true, budget_usd: undefined });
	});
	it("needs a note to reject", () => {
		expect(canDecide("accepted", "")).toBe(true);
		expect(canDecide("rejected", "  ")).toBe(false);
		expect(canDecide("rejected", "rubric too loose")).toBe(true);
	});
	it("renders the quality report", () => {
		const t = i18n.getFixedT("en", "authoring");
		const report: QualityReport = {
			suite: "s",
			tasks: 3,
			library_tasks: 835,
			errors: 1,
			warnings: 1,
			summary: "",
			duplicates: [{ a: "s/a", b: "s/b", kind: "near", jaccard: 0.86 }],
			contamination: [{ task: "s/c", split: "held_out", overlap: 0.5, matches: ["lib/x"], severity: "error" }],
		};
		const html = text(render(<QualityBody report={report} t={t as never} />));
		expect(html).toContain("86% the same");
		expect(html).toContain("50% of its text, like lib/x");
		expect(html).toContain("held out, but already public");
		const clean = text(render(<QualityBody report={{ ...report, duplicates: null, contamination: [] }} t={t as never} />));
		expect(clean).toContain("nothing found in 835 public tasks");
	});
	it("points an empty queue to the Policy tab, where policy drafts are made", () => {
		queue.data = { ...generated, pending: 0, accepted: 0, tasks: [] };
		try {
			const html = render(<ReviewTab suite={qa} />);
			expect(html).toContain("No drafts to review yet. Policy drafts are made on the Policy tab of a support benchmark.");
			expect(html).toMatch(/<a[^>]*href="\/suites\/support-faq\?tab=policy"[^>]*data-testid="suite-review-queue-policy-link"/);
		} finally {
			queue.data = generated;
		}
	});
});