import "@/lib/i18n";

import type { TraceProposal } from "@/lib/store/apis/traceDraftApi";
import type { InboxSession } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GuideFields } from "./inboxGuide";
import { guideDefaults } from "./inboxGuide.utils";
import { prefillFromProposal, submitReview } from "./proposalBar";
import { SessionImport } from "./sessionImport";

const state = vi.hoisted(() => ({ proposal: undefined as unknown }));

vi.mock("@/lib/store/apis/traceDraftApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/traceDraftApi")>();
	return {
		...actual,
		useGetInboxProposalQuery: () => ({ data: state.proposal, refetch: () => undefined }),
		useImportInboxSessionMutation: () => [() => ({ unwrap: async () => ({}) }), { isLoading: false }],
	};
});
vi.mock("@/lib/store/apis/inboxApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/inboxApi")>();
	return { ...actual, useAcceptInboxItemMutation: () => [() => ({ unwrap: async () => ({}) }), { isLoading: false }] };
});
vi.mock("@/lib/store/apis/jobRunApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/jobRunApi")>();
	return { ...actual, useCreateJobMutation: () => [() => ({ unwrap: async () => ({ id: "j" }) }), { isLoading: false }] };
});
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@/lib/store/apis/platformApi")>();
	return { ...actual, useGetJobQuery: () => ({ data: undefined }) };
});

// Made-up session and proposal (never a real session file).
const session: InboxSession = {
	requests: ["Refund order A-1 please"],
	calls: [{ seq: 1, tool: "refund_order", args: '{"order":"A-1"}', is_error: true, result: "not found" }],
	final_answer: "I could not.",
};

const PROPOSAL: TraceProposal = {
	item: "inc-000000000001",
	kind: "tool-use",
	guide: {
		calls: [{ tool: "refund_order", args: { order: "A-1" } }],
		forbidden: [{ tool: "refund_order", args: { order: "A-9" } }],
		reply_rubric: "Says the refund went through.",
	},
	reasons: [
		{ field: "calls", text: "The refund call failed.", quotes: [{ run_id: "inc-000000000001", seq: 1, text: "not found in the shop" }] },
	],
	origin: "model",
	model: "openai/cheap",
	cost_usd: 0.01,
	dropped_unverified: 2,
	created_at: "2026-11-27T10:00:00Z",
};

beforeEach(() => {
	state.proposal = undefined;
});

describe("review screen with a model draft (wave 4, trace to environment)", () => {
	it("prefills the tool fields from a proposal: right calls, forbidden calls as wrong, the rubric", () => {
		const patch = prefillFromProposal(PROPOSAL, "tool-use");
		expect(patch.mode).toBe("guided");
		expect(patch.calls).toEqual([
			{ tool: "refund_order", args: '{"order":"A-1"}', verdict: "right", added: true },
			{ tool: "refund_order", args: '{"order":"A-9"}', verdict: "wrong", added: true },
		]);
		expect(patch.replyRubric).toBe("Says the refund went through.");
		const qa = prefillFromProposal({ ...PROPOSAL, guide: { answers: ["30 days", "full refund"], recorded_wrong: true } }, "qa");
		expect(qa.answers).toBe("30 days\nfull refund");
		expect(qa.recordedWrong).toBe(true);
		expect(qa.calls).toBeUndefined();
	});

	it("shows the bar only with an item and a benchmark, and labels each suggested field", () => {
		const values = guideDefaults(session, false);
		const without = renderToStaticMarkup(<GuideFields kind="tool-use" session={session} values={values} errors={[]} onChange={() => {}} />);
		expect(without).not.toContain('data-testid="proposal-bar"');
		state.proposal = PROPOSAL;
		const html = renderToStaticMarkup(
			<GuideFields
				kind="tool-use"
				session={session}
				values={values}
				errors={[]}
				onChange={() => {}}
				itemId={PROPOSAL.item}
				suite="support"
			/>,
		);
		expect(html).toContain("Draft with a model");
		expect(html).toContain("Approve and add to the benchmark");
		expect(html).toContain("Save without checking");
		expect(html).toContain("Suggested by a model");
		expect(html).toContain('data-testid="proposal-field-calls"');
		expect(html).toContain("The refund call failed.");
		// The rubric and the forbidden call have no reason left: the person checks them.
		expect(html).toContain('data-testid="proposal-noquote-reply_rubric"');
		expect(html).toContain("No quote found for this; check it yourself.");
		expect(html).toContain("2 suggestions had no quote found in the session and were left out.");
	});

	it("approve posts the accept with the edited guide, then the readiness job", async () => {
		const calls: string[] = [];
		const values = { ...guideDefaults(session, false), replyRubric: "Says sorry" };
		const out = await submitReview({
			itemId: PROPOSAL.item,
			suite: "support",
			kind: "tool-use",
			values,
			check: true,
			accept: async ({ id, body }) => {
				calls.push(`accept ${id} ${body.suite} ${body.guide?.reply_rubric}`);
				return { draft: { finished: true, task: "support/refund-a-1" } };
			},
			createJob: async (body) => {
				calls.push(`job ${body.kind} ${JSON.stringify(body.params)}`);
				return { id: "job-1" };
			},
		});
		expect(calls).toEqual([`accept ${PROPOSAL.item} support Says sorry`, 'job suite.admit {"suite":"support","task":"refund-a-1"}']);
		expect(out.admitJob).toBe("job-1");
	});

	it("save without checking posts the accept only", async () => {
		let jobs = 0;
		const out = await submitReview({
			itemId: PROPOSAL.item,
			suite: "support",
			kind: "tool-use",
			values: guideDefaults(session, false),
			check: false,
			accept: async () => ({ draft: { finished: true, task: "support/x" } }),
			createJob: async () => {
				jobs++;
				return { id: "j" };
			},
		});
		expect(jobs).toBe(0);
		expect(out.admitJob).toBeUndefined();
	});

	it("offers the session file import with its help text", () => {
		const html = renderToStaticMarkup(<SessionImport />);
		expect(html).toContain("Import a session file");
		expect(html).toContain('accept=".jsonl"');
		expect(html).toContain("The file is cleaned of keys, emails and home folder names before it is kept.");
	});
});