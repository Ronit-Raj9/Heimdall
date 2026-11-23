import "@/lib/i18n";

import type { MaximTaskDraft } from "@/lib/types/maximFailures";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { TaskReviewCard } from "./maximFailuresParts";

// Fix wave 3, N33: a reviewed Maxim task names its reviewer; a member never reads an email.

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const task: MaximTaskDraft = {
	cluster: "c_1",
	item: "inc-1",
	suite: "answers",
	id: "inc-1",
	kind: "qa",
	state: "accepted",
	generated: ["reply"],
	request: "What is the return window for shoes?",
	recorded: "90 days.",
	suggested_rubric: "The agent resolves the request.",
	readiness: { status: "not_run" },
	drafted: "2026-11-22T09:00:00Z",
};

const card = (t: MaximTaskDraft & { reviewed_by_name?: string }) =>
	renderToStaticMarkup(<TaskReviewCard task={t} onAccept={() => {}} onReject={() => {}} onRerun={() => {}} />);

describe("TaskReviewCard reviewer", () => {
	it("prints the reviewer's name, not the stored email", () => {
		const member = card({ ...task, reviewed_by_name: "Ada" });
		expect(member).toContain("Reviewed by Ada");
		const admin = card({ ...task, reviewed_by: "ada@example.com", reviewed_by_name: "Ada" });
		expect(admin).toContain("Reviewed by Ada");
		expect(admin).not.toContain("Reviewed by ada@example.com");
	});
});