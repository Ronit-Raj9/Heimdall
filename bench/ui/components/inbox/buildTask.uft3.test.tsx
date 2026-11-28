import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import fixture from "../../../handlers/testdata/tracetask_result_t3.json";
import { BuildTaskView } from "./buildTask";
import { buildTaskBody, type BuildTaskResult, type BuildTaskState } from "./buildTaskApi";

const result = fixture as unknown as BuildTaskResult;
const ready: BuildTaskState = { job_id: "job_a", status: "succeeded", result, can_approve: true, can_retry: false, budget_usd: 0.5 };

describe("Build a task (round 7 lane T3)", () => {
	it("posts the body the handler takes", () => {
		expect(buildTaskBody("support-evals", "gpt-6-luna", 0.5)).toEqual({ suite: "support-evals", model: "gpt-6-luna", budget_usd: 0.5 });
	});
	it("renders the contract's result: what went wrong, quotes, shape, instruction, checks, variants, admission, cost", () => {
		const html = render(<BuildTaskView state={ready} />);
		expect(html).toContain("False claim in the answer");
		expect(html).toContain("Your refund has been issued.");
		expect(html).toContain("1 quote not found in the trace was dropped.");
		expect(html).toContain("Task shape: tool-use");
		expect(html).toContain("Issue it, and tell the customer the true outcome.");
		expect(html).toContain("tests/checks.json");
		expect(html).toContain("claims_success_after_timeout");
		expect(html).toContain('data-testid="build-task-control-wrong_order_id"');
		expect(html).toContain("Cost $0.12 in 4 model calls");
		expect(html).toContain('data-testid="build-task-ready"');
	});
	it("shows a refused draft with the failing control", () => {
		const html = render(<BuildTaskView state={{ ...ready, can_approve: false, can_retry: true, failing: ["wrong_order_id want fail got pass"] }} />);
		expect(html).toContain("Not admitted: wrong_order_id want fail got pass");
		expect(html).not.toContain('data-testid="build-task-ready"');
	});
	it("shows the running step", () => {
		expect(render(<BuildTaskView state={{ job_id: "j", status: "running", step: "diagnose", can_approve: false, can_retry: false }} />)).toContain("Building: diagnose");
	});
});
