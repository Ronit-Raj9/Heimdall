import "@/lib/i18n";

import type { InboxSession } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { INBOX_LIST_FIXTURE, INBOX_NEW_DETAIL_FIXTURE } from "./inbox.fixtures";
import { AcceptForm } from "./inboxActions";
import { bulkAcceptErrors, bulkRequestFrom } from "./inboxBulkAccept";
import { GuideFields } from "./inboxGuide";
import { guideDefaults, guideFormErrors, guideFrom, parseArgs, prettyArgs } from "./inboxGuide.utils";

const noop = () => {};
const session: InboxSession = {
	requests: ["Refund order A-1 please", "Also it was charged twice"],
	calls: [
		{ seq: 1, tool: "lookup_order", args: '{"order":"A-1"}', is_error: false, result: "{}" },
		{ seq: 2, tool: "issue_refund", args: '{"order":"A-1"}', is_error: true, result: "timeout" },
	],
	final_answer: "Done.",
};

describe("guide helpers", () => {
	it("starts from the recorded session: failed calls wrong, later turns kept, recorded answer wrong", () => {
		const v = guideDefaults(session, false);
		expect(v.mode).toBe("guided");
		expect(v.keepTurns).toBe(true);
		expect(v.calls.map((c) => c.verdict)).toEqual(["right", "wrong"]);
		expect(v.recordedWrong).toBe(true);
		expect(v.answers).toBe("");
	});

	it("takes a golden's recording as right", () => {
		const v = guideDefaults(session, true);
		expect(v.calls.every((c) => c.verdict === "right")).toBe(true);
		expect(v.answers).toBe("Done.");
		expect(v.recordedWrong).toBe(false);
	});

	it("offers only a draft without a session", () => {
		expect(guideDefaults(undefined, false).mode).toBe("draft");
	});

	it("sends right calls, wrong calls as forbidden, the order and the rubric for tool tasks", () => {
		const v = { ...guideDefaults(session, false), replyRubric: " Says sorry " };
		v.calls = [...v.calls, { tool: "notify_customer", args: '{"order":"A-1"}', verdict: "right", added: true }];
		expect(guideFormErrors(v, "mcp")).toEqual([]);
		expect(guideFrom(v, "mcp")).toEqual({
			keep_turns: true,
			calls: [
				{ tool: "lookup_order", args: { order: "A-1" } },
				{ tool: "notify_customer", args: { order: "A-1" } },
			],
			forbidden: [{ tool: "issue_refund", args: { order: "A-1" } }],
			ordered: true,
			reply_rubric: "Says sorry",
		});
	});

	it("sends one answer per line for qa, never tool fields", () => {
		const v = { ...guideDefaults(session, false), answers: "Refund issued\n\n 5 days \n" };
		expect(guideFrom(v, "qa")).toEqual({ keep_turns: true, answers: ["Refund issued", "5 days"], recorded_wrong: true });
	});

	it("refuses what leaves the task without a check", () => {
		const v = guideDefaults(session, false);
		expect(guideFormErrors(v, "qa")).toEqual(["answers"]);
		expect(guideFormErrors({ ...v, calls: v.calls.map((c) => ({ ...c, verdict: "skip" as const })) }, "tool-use")).toEqual(["calls"]);
		expect(guideFormErrors({ ...v, calls: [{ tool: "", args: "{", verdict: "right", added: true }] }, "mcp")).toEqual(["tool", "args"]);
		expect(guideFormErrors({ ...v, mode: "draft" }, "qa")).toEqual([]);
		expect(guideFrom({ ...v, mode: "draft" }, "qa")).toBeUndefined();
	});

	it("reads arguments as a JSON object or nothing", () => {
		expect(parseArgs("")).toEqual({ ok: true });
		expect(parseArgs("{}")).toEqual({ ok: true });
		expect(parseArgs('{"a":1}')).toEqual({ ok: true, args: { a: 1 } });
		expect(parseArgs("[1]").ok).toBe(false);
		expect(parseArgs("nope").ok).toBe(false);
		expect(prettyArgs("{}")).toBe("");
		expect(prettyArgs("not json")).toBe("not json");
	});
});

describe("GuideFields", () => {
	it("lists the recorded calls with right and wrong choices and the grader switch", () => {
		const html = renderToStaticMarkup(
			<GuideFields kind="mcp" session={session} values={guideDefaults(session, false)} errors={[]} onChange={noop} />,
		);
		expect(html).toContain("What should have happened?");
		expect(html).toContain('data-testid="inbox-guide-call-1"');
		expect(html).toContain('data-testid="inbox-guide-call-1-wrong"');
		expect(html).toContain('data-testid="inbox-guide-switch-grader"');
		expect(html).toContain("Keep the user&#x27;s later message in the instruction");
	});

	it("asks for the right answer for qa and shows the recorded one", () => {
		const html = renderToStaticMarkup(
			<GuideFields kind="qa" session={session} values={guideDefaults(session, false)} errors={["answers"]} onChange={noop} />,
		);
		expect(html).toContain('data-testid="inbox-guide-answers"');
		expect(html).toContain('data-testid="inbox-guide-error-answers"');
		expect(html).toContain('data-testid="inbox-guide-recorded"');
		expect(html).not.toContain('data-testid="inbox-guide-calls"');
	});

	it("is part of the accept form when the session is known", () => {
		const item = INBOX_NEW_DETAIL_FIXTURE.item;
		const html = renderToStaticMarkup(<AcceptForm item={item} session={session} suites={INBOX_LIST_FIXTURE.suites} onSubmit={noop} />);
		expect(html).toContain('data-testid="inbox-guide"');
		expect(html).toContain('data-testid="inbox-guide-mode-guided"');
		const without = renderToStaticMarkup(<AcceptForm item={item} suites={INBOX_LIST_FIXTURE.suites} onSubmit={noop} />);
		expect(without).toMatch(/data-testid="inbox-guide-mode-guided"[^>]*disabled=""|disabled=""[^>]*data-testid="inbox-guide-mode-guided"/);
	});
});

describe("bulk accept", () => {
	const base = { ids: ["inc-1", "inc-2"], suite: "support", recorded: false, rubric: "", switchGrader: true };
	it("sends drafts without a shared setting, and a guide with one", () => {
		expect(bulkRequestFrom(base)).toEqual({ ids: ["inc-1", "inc-2"], suite: "support" });
		expect(bulkRequestFrom({ ...base, recorded: true })).toEqual({
			ids: ["inc-1", "inc-2"],
			suite: "support",
			guide: { use_recorded: true },
			switch_grader: true,
		});
		expect(bulkRequestFrom({ ...base, rubric: " Polite " }).guide).toEqual({ reply_rubric: "Polite" });
	});
	it("needs items and a benchmark", () => {
		expect(bulkAcceptErrors({ ...base, ids: [], suite: "" })).toEqual(["none", "suite"]);
	});
});
