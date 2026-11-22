import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { draftTarget, InboxDraftNext } from "./inboxDraftNext";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));

const draft = { task: "support-faq/charged-twice", dir: "benchmarks/support-faq/tasks/charged-twice", template: "qa", edit_me: [], finished: false, next: [] };

describe("InboxDraftNext", () => {
	it("links to the task and runs its readiness check", () => {
		const html = render(<InboxDraftNext draft={draft} />);
		expect(html).toContain('href="/suites/support-faq?tab=edit"');
		expect(html).toContain('data-testid="job-run-suite.admit"');
		expect(html).toContain("Run its readiness check");
	});
	it("needs suite/task", () => {
		expect(draftTarget({ task: "x" })).toBeNull();
		expect(draftTarget(draft)).toEqual({ suite: "support-faq", task: "charged-twice" });
		expect(render(<InboxDraftNext draft={{ ...draft, task: "x" }} />)).toBe("");
	});
});
