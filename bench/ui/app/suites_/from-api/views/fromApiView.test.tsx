import "@/lib/i18n";

import type { AppTasksDraft } from "@/lib/types/appTasks";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { DraftDetail, DraftList, PlanTable } from "./fromApiView";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

const { mutation } = vi.hoisted(() => ({
	mutation: () => [() => undefined, { isLoading: false, error: undefined, data: undefined, reset: () => undefined }],
}));
vi.mock("@/lib/store/apis/appTasksApi", () => ({
	useApproveAppTaskRulesMutation: mutation,
	useCreateAppTaskDraftMutation: mutation,
	useWriteAppTasksMutation: mutation,
	useReviewAppTaskMutation: mutation,
	useGetAppTaskDraftQuery: () => ({ isLoading: true }),
	useGetAppTaskDraftsQuery: () => ({ isLoading: true }),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: mutation }));
vi.mock("@/components/platform/confirmDialog", () => ({ useConfirm: () => async () => true }));
vi.mock("@/lib/session/sessionContext", () => ({ useSession: () => ({}), canUse: () => true }));

const base: AppTasksDraft = {
	id: "ag_0123456789ab",
	suite: "shop-support",
	app: "shop",
	title: "Acme Shop API",
	status: "generated",
	model: { provider: "openai", model: "gpt-5" },
	rules: [{ id: "R1", text: "Never refund an order delivered more than 30 days ago.", kind: "must_not", operations: ["updateOrder"] }],
	approved_by: "ana@example.com",
	judged: false,
	plans: [
		{
			scenario: {
				rule_id: "R1",
				title: "Refuse an old refund",
				opening_message: "Refund O-2 please",
				persona_goal: "Refund",
				expected_actions: [{ tool: "getOrder", args: { order_id: "O-2" } }],
			},
			checks: 3,
			summary: ["calls getOrder", "never updateOrder"],
			proof: { reference: "passes", controls: { empty: "x", "also updateOrder": "y" } },
		},
	],
	dropped: [{ rule_id: "R1", title: "Refund a missing order", reason: "reference step 1 (getOrder) fails on the seed with 404" }],
	written: [],
	calls: [{ op: "policy_rules", model: "openai/gpt-5", attempts: 1, usd: 0.01 }],
	spent_usd: 0.0123,
	warnings: [],
	operations: [{ name: "getOrder", method: "GET", path: "/orders/{order_id}", kind: "get" }],
	collections: ["orders"],
	next: "Check the drafted tasks.",
};

describe("FromApiView", () => {
	it("lists drafts with their state", () => {
		const html = renderToStaticMarkup(
			<DraftList
				drafts={[
					{
						id: "ag_0123456789ab",
						suite: "shop-support",
						app: "shop",
						title: "Acme",
						status: "written",
						rules: 4,
						tasks: 6,
						written: 6,
						pending: 2,
						spent_usd: 0.2,
						created_at: "",
					},
				]}
				onOpen={() => undefined}
			/>,
		);
		expect(html).toContain("In the benchmark");
		expect(html).toContain("4 rules, 6 tasks, 2 waiting for review");
		expect(renderToStaticMarkup(<DraftList drafts={[]} onOpen={() => undefined} />)).toContain("from-api-drafts-empty");
	});

	it("shows drafted tasks, what each catches, and why others were dropped", () => {
		const html = renderToStaticMarkup(<DraftDetail draft={base} writable onBack={() => undefined} onChanged={() => undefined} />);
		expect(html).toContain("Refuse an old refund");
		expect(html).toContain("also updateOrder");
		expect(html).toContain("fails on the seed with 404");
		expect(html).toContain('data-testid="from-api-write"');
		expect(html).toContain("Write 1 tasks");
		expect(html).toContain("$0.0123 spent");
		expect(html).not.toMatch(/\b(probe|canary|sealed|lease|runner)\b/i);
	});

	it("asks for rule approval before tasks, and offers review once written", () => {
		const drafted = renderToStaticMarkup(
			<DraftDetail
				draft={{ ...base, status: "rules_drafted", plans: [], dropped: [] }}
				writable
				onBack={() => undefined}
				onChanged={() => undefined}
			/>,
		);
		expect(drafted).toContain('data-testid="from-api-approve"');
		expect(drafted).not.toContain('data-testid="from-api-tasks"');
		const written = renderToStaticMarkup(
			<DraftDetail
				draft={{
					...base,
					status: "written",
					written: [
						{ id: "refuse-an-old-refund", title: "Refuse an old refund", rule_id: "R1", checks: 3, summary: "s", review: "pending" },
					],
				}}
				writable
				onBack={() => undefined}
				onChanged={() => undefined}
			/>,
		);
		expect(written).toContain("Pending review");
		expect(written).toContain('data-testid="from-api-accept-refuse-an-old-refund"');
		expect(written).not.toContain('data-testid="from-api-write"');
	});

	it("renders the plan table", () => {
		expect(renderToStaticMarkup(<PlanTable plans={base.plans} />)).toContain("3: calls getOrder; never updateOrder");
	});
});