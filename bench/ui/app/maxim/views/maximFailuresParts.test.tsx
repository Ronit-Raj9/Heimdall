import "@/lib/i18n";

import type { MaximFailureCluster, MaximTaskDraft } from "@/lib/types/maximFailures";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import {
	ClusterList,
	DEFAULT_FAILURE_FORM,
	FailureCounts,
	TaskReviewCard,
	draftedClusters,
	filterFromForm,
	needsAnswer,
	readinessTone,
	splitNames,
	stateTone,
} from "./maximFailuresParts";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const cluster: MaximFailureCluster = {
	id: "c_1",
	label: "refund_order fails: error: refund service down",
	tool: "refund_order",
	items: ["inc-1", "inc-2"],
	occurrences: 7,
	sample: "Refund order A1 please",
	reasons: ["trace errored in Maxim"],
	links: ["https://app.getmaxim.ai/workspace/ws1/log-repository/repo1/logs?traceId=mx-1"],
	kind: "tool-use",
};

const task: MaximTaskDraft = {
	cluster: "c_1",
	item: "inc-1",
	suite: "answers",
	id: "inc-1",
	kind: "qa",
	state: "pending_review",
	generated: ["reply"],
	request: "What is the return window for shoes?",
	recorded: "90 days.",
	suggested_rubric: "The agent resolves the request without the failure seen in production.",
	readiness: { status: "not_run" },
	drafted: "2026-11-22T09:00:00Z",
};

function withRouter(node: ReactNode): string {
	return renderToStaticMarkup(node);
}

describe("Maxim failures", () => {
	it("builds the filter from the form", () => {
		expect(splitNames(" a, b ,a,, ")).toEqual(["a", "b"]);
		expect(filterFromForm({ ...DEFAULT_FAILURE_FORM, errors: false })).toBeNull();
		expect(filterFromForm(DEFAULT_FAILURE_FORM)).toEqual({ errors: true, days: 7, limit: 200 });
		expect(
			filterFromForm({ ...DEFAULT_FAILURE_FORM, errors: false, evaluators: "Faithfulness", scoreBelow: "0.5", scoreEvaluators: "Helpfulness", days: "3" }),
		).toEqual({ evaluators: ["Faithfulness"], score_below: 0.5, score_evaluators: ["Helpfulness"], days: 3, limit: 200 });
		expect(filterFromForm({ ...DEFAULT_FAILURE_FORM, errors: false, scoreBelow: "x" })).toBeNull();
	});
	it("maps states to tones and knows which tasks need an answer", () => {
		expect(stateTone("accepted")).toBe("ok");
		expect(stateTone("needs_files")).toBe("warn");
		expect(readinessTone("not_ready")).toBe("bad");
		expect(readinessTone("queued")).toBe("running");
		expect(needsAnswer("qa")).toBe(true);
		expect(needsAnswer("tool-use")).toBe(false);
		expect([...draftedClusters([task, { ...task, cluster: "c_2", state: "rejected" }])]).toEqual(["c_1"]);
	});
	it("shows counts and clusters with links back to Maxim", () => {
		const counts = { scanned: 50, failed: 12, evaluator_failed: 4, errored: 8, low_score: 0, fetched: 12, new_items: 9, repeats: 3 };
		expect(renderToStaticMarkup(<FailureCounts counts={counts} />)).toContain("Failures");
		const html = renderToStaticMarkup(<ClusterList clusters={[cluster]} selected={new Set(["c_1"])} drafted={new Set()} onToggle={() => {}} />);
		expect(html).toContain("refund_order fails");
		expect(html).toContain("7 traces");
		expect(html).toContain("Open in Maxim");
		expect(html).toContain("checked");
		expect(renderToStaticMarkup(<ClusterList clusters={[]} selected={new Set()} drafted={new Set()} onToggle={() => {}} />)).toContain("No failures matched");
	});
	it("asks a question and answer task for the right answer", () => {
		const html = withRouter(<TaskReviewCard task={task} onAccept={() => {}} onReject={() => {}} onRerun={() => {}} />);
		expect(html).toContain("Waiting for review");
		expect(html).toContain("The right answer");
		expect(html).toContain("What should have happened");
		expect(html).toContain("90 days.");
		expect(html).toContain("maxim-task-accept");
		expect(html).not.toContain("maxim-task-rerun");
	});
	it("shows readiness and the files left to finish after review", () => {
		const accepted = withRouter(
			<TaskReviewCard task={{ ...task, state: "accepted", readiness: { status: "queued", job_id: "j1" }, reviewed_by: "ana" }} onAccept={() => {}} onReject={() => {}} onRerun={() => {}} />,
		);
		expect(accepted).toContain("Checking");
		expect(accepted).toContain("Reviewed by ana");
		expect(accepted).not.toContain("maxim-task-accept");
		const files = withRouter(
			<TaskReviewCard task={{ ...task, kind: "tool-use", state: "needs_files", edit_me: ["verifier/checks.py"] }} onAccept={() => {}} onReject={() => {}} onRerun={() => {}} />,
		);
		expect(files).toContain("Finish these files first: verifier/checks.py");
		expect(files).toContain("maxim-task-rerun");
	});
});
