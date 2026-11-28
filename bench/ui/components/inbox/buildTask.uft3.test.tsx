import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import fixture from "../../../handlers/testdata/tracetask_result_t3.json";
import { BuildTaskActions, BuildTaskView } from "./buildTask";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("./buildTaskApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("./buildTaskApi")>()),
	useDecideBuildTaskMutation: () => mutation(),
	useStartBuildTaskMutation: () => mutation(),
}));
import { buildTaskBody, type BuildTaskResult, type BuildTaskState } from "./buildTaskApi";

const result = fixture as unknown as BuildTaskResult;
const ready: BuildTaskState = { job_id: "job_a", status: "succeeded", result, can_approve: true, can_retry: false, budget_usd: 0.5 };

describe("Build a task (round 7 lane T3)", () => {
	it("posts the body the handler takes", () => {
		expect(buildTaskBody("support-evals", "gpt-6-luna", 0.5, "openai")).toEqual({ suite: "support-evals", provider: "openai", model: "gpt-6-luna", budget_usd: 0.5 });
	});
	it("renders the contract's result: what went wrong, quotes, shape, instruction, checks, variants, admission, cost", () => {
		const html = render(<BuildTaskView state={ready} />);
		expect(html).toContain("False claim in the answer");
		expect(html).toContain("Your refund has been issued.");
				expect(html).toContain("Task shape: mcp");
		expect(html).toContain("verifier/tests/checks.py");
		expect(html).toContain('data-testid="build-task-variant-no-call"');
		expect(html).toContain("says the refund was issued after the lookup failed");
		expect(html).toContain('data-testid="build-task-control-variant_no_call"');
		expect(html).toContain("in 2 model calls");
		expect(html).toContain('data-testid="build-task-ready"');
	});
	it("shows a refused draft with the failing control", () => {
		const html = render(<BuildTaskView state={{ ...ready, can_approve: false, can_retry: true, failing: ["variant_no_call want fail got pass"] }} />);
		expect(html).toContain("Not admitted: variant_no_call want fail got pass");
		expect(html).not.toContain('data-testid="build-task-ready"');
	});
	it("shows the running step", () => {
		expect(render(<BuildTaskView state={{ job_id: "j", status: "running", step: "diagnose", can_approve: false, can_retry: false }} />)).toContain("Building: diagnose");
	});
	it("offers Approve with what it means only when the server offers it, and Try again on a refused draft", () => {
		const ok = render(<BuildTaskActions itemId="inc-1" state={ready} onChanged={() => {}} />);
		expect(ok).toContain('data-testid="build-task-approve"');
		expect(ok).toContain("The task joins support.");
		expect(ok).not.toContain('data-testid="build-task-retry"');
		const refused = render(<BuildTaskActions itemId="inc-1" state={{ ...ready, can_approve: false, can_retry: true }} onChanged={() => {}} />);
		expect(refused).not.toContain('data-testid="build-task-approve"');
		expect(refused).toContain('data-testid="build-task-retry"');
		expect(refused).toContain('data-testid="build-task-reject"');
		const decided = render(<BuildTaskActions itemId="inc-1" state={{ ...ready, decision: { job_id: "job_a", decision: "approved", by: "a", at: "" } }} onChanged={() => {}} />);
		expect(decided).toBe("");
	});
	it("opens a quote in the session and shows the finalize states after approval", () => {
		expect(render(<BuildTaskView state={ready} onQuote={() => {}} />)).toContain('data-testid="build-task-quote-open"');
		const approved = { ...ready, can_approve: false, decision: { job_id: "job_a", decision: "approved" as const, by: "ada", at: "" } };
		expect(render(<BuildTaskView state={{ ...approved, finalize: { job_id: "job_b", status: "running", admitted: false } }} />)).toContain("Checking the task.");
		const ok = render(<BuildTaskView state={{ ...approved, finalize: { job_id: "job_b", status: "succeeded", admitted: true } }} />);
		expect(ok).toContain("Ready in support.");
		expect(ok).toContain('href="/suites/support?tab=edit"');
		const refused = { ...approved, can_retry: true, failing: ["variant_no_call want fail got pass"], finalize: { job_id: "job_b", status: "succeeded", admitted: false, failing: ["variant_no_call want fail got pass"] } };
		expect(render(<BuildTaskView state={refused} />)).toContain("admission refused it: variant_no_call want fail got pass");
		const acts = render(<BuildTaskActions itemId="inc-1" state={refused} onChanged={() => {}} />);
		expect(acts).toContain('data-testid="build-task-retry"');
		expect(acts).not.toContain('data-testid="build-task-reject"');
	});
});
