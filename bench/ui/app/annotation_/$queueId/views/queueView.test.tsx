import "@/lib/i18n";

import { LOCAL_SESSION } from "@/lib/session/sessionContext";
import { mutation, okQuery, render } from "@/lib/testing/render";
import type { AnnotationAgreement, AnnotationItem, AnnotationQueue } from "@/lib/types/annotation";
import { describe, expect, it, vi } from "vitest";
import { QueueTable, parseLabellers, queueRequest } from "@/app/annotation/views/annotationView";
import type { ExperimentStatus } from "@/lib/types/bench";
import {
	AddItems,
	AgreementPanel,
	ItemTable,
	Labeller,
	QueueScreen,
	SettingsTab,
	addItemsBody,
	finishedRunsOf,
	keyAction,
} from "./queueView";

const exps = vi.hoisted(() => ({ list: [] as unknown[] }));

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: () => [null, () => undefined],
}));
vi.mock("@/lib/store/apis/annotationApi", () => ({
	useGetAnnotationQueuesQuery: () => okQuery({ queues: [] }),
	useCreateAnnotationQueueMutation: () => mutation(),
	useGetAnnotationQueueQuery: () => okQuery(undefined),
	useGetAnnotationItemsQuery: () => okQuery({ items: [], total: 0 }),
	useGetAnnotationAgreementQuery: () => okQuery(undefined),
	useAddAnnotationItemsMutation: () => mutation(),
	useAssignAnnotationItemsMutation: () => mutation(),
	useNextAnnotationItemMutation: () => mutation(),
	useLabelAnnotationItemMutation: () => mutation(),
	useResolveAnnotationItemMutation: () => mutation(),
	useUpdateAnnotationQueueMutation: () => mutation(),
	useDeleteAnnotationQueueMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/benchApi", async (orig) => ({
	...(await orig<typeof import("@/lib/store/apis/benchApi")>()),
	useGetSuitesQuery: () => okQuery({ suites: [] }),
	useListExperimentsQuery: () => okQuery({ experiments: exps.list }),
}));

const queue: AnnotationQueue = {
	id: "q_1",
	name: "Refund answers",
	benchmark: "support-faq",
	kind: "runs",
	reviewers_per_item: 2,
	blind: true,
	assignment: "open",
	created_by: "u_1",
	created_at: "2026-11-20T10:00:00Z",
	updated_at: "2026-11-20T10:00:00Z",
	counts: { total: 10, open: 4, labelling: 2, disputed: 1, done: 3, calibrated: 3 },
	mine: 6,
	can_manage: true,
	can_label: true,
};

const item = (over: Partial<AnnotationItem>): AnnotationItem => ({
	id: "i_1",
	state: "open",
	task: "refund-window",
	answer: "30 days",
	labels: [],
	label_count: 0,
	added_at: "2026-11-20T10:00:00Z",
	...over,
});

const text = (html: string) => html.replace(/<[^>]+>/g, " ");

describe("labelling queues", () => {
	it("lists queues with progress, disagreements and items for the caller", () => {
		const html = render(<QueueTable queues={[queue]} />);
		expect(html).toContain('href="/annotation/q_1"');
		expect(text(html)).toContain("3 / 10");
		expect(text(html)).toContain("2 per item, blind");
		expect(html).toContain("support-faq");
	});

	it("builds the create body and clamps people per item", () => {
		expect(
			queueRequest({
				name: " Refunds ",
				benchmark: "",
				kind: "judge",
				reviewers: 9,
				blind: false,
				assignment: "fixed",
				labellers: "u_1, u_2 u_1",
				instructions: "  ",
			}),
		).toEqual({ name: "Refunds", kind: "judge", reviewers_per_item: 5, blind: false, assignment: "fixed", reviewers: ["u_1", "u_2"] });
		expect(parseLabellers("")).toEqual([]);
	});

	it("maps keys to actions", () => {
		expect(keyAction("1")).toBe("correct");
		expect(keyAction("C")).toBe("correct");
		expect(keyAction("2")).toBe("incorrect");
		expect(keyAction("n")).toBe("note");
		expect(keyAction("j")).toBe("next");
		expect(keyAction("q")).toBeNull();
	});

	it("opens on the labeller with the counts and the key help", () => {
		const html = render(<QueueScreen queue={queue} />);
		expect(html).toContain('data-testid="annotation-stat-disputed"');
		expect(html).toContain('data-testid="annotation-start"');
		const labeller = render(<Labeller queue={{ ...queue, instructions: "Correct means the policy is quoted." }} />);
		expect(labeller).toContain("Correct means the policy is quoted.");
		expect(text(html)).not.toMatch(/sealed|suite|probe|canary|lease/i);
	});

	it("tells a viewer and an empty queue what to do", () => {
		expect(render(<Labeller queue={queue} />, { ...LOCAL_SESSION, authMode: "hosted", signedIn: true, role: "viewer" })).toContain(
			'data-testid="annotation-label-cannot"',
		);
		expect(render(<Labeller queue={{ ...queue, counts: { ...queue.counts, total: 0 } }} />)).toContain(
			'data-testid="annotation-label-no-items"',
		);
	});

	it("shows labels, hidden counts and decisions on items", () => {
		const html = render(
			<ItemTable
				queue={queue}
				items={[
					item({
						id: "i_1",
						state: "disputed",
						labels: [
							{ user_id: "u_1", email: "a@x.io", label: "correct", at: "2026-11-20T10:00:00Z" },
							{ user_id: "u_2", label: "incorrect", note: "wrong policy", at: "2026-11-20T10:01:00Z" },
						],
						label_count: 2,
					}),
					item({ id: "i_2", state: "labelling", labels: null, label_count: 1 }),
					item({ id: "i_3", state: "done", resolution: { label: "correct", method: "consensus", at: "2026-11-20T10:00:00Z" } }),
				]}
			/>,
		);
		expect(html).toContain('data-testid="annotation-decide-correct-i_1"');
		expect(text(html)).toContain("wrong policy");
		expect(text(html)).toContain("hidden until you label");
		expect(text(html)).toContain("Disagreement");
		expect(text(html)).toContain("agreed");
	});

	it("shows kappa with its reading and the judge against people", () => {
		const data: AnnotationAgreement = {
			queue: "q_1",
			agreement: {
				multi_labelled: 8,
				unanimous: 6,
				percent_agreement: 0.75,
				cohen: [{ a: "u_1", b: "u_2", items: 8, kappa: 0.5, agree: 6 }],
				cohen_mean: 0.5,
				fleiss: 0.48,
				fleiss_items: 8,
				interpretation: "moderate",
				judge: { items: 5, agree: 4, rate: 0.8, kappa: 0.6, false_passes: 1, false_fails: 0 },
			},
			labellers: { u_1: "a@x.io", u_2: "b@x.io" },
		};
		const html = text(render(<AgreementPanel data={data} />));
		expect(html).toContain("75%");
		expect(html).toContain("0.48");
		expect(html).toContain("moderate");
		expect(html).toContain("a@x.io");
		expect(html).toContain("80%");
		const none = render(<AgreementPanel data={{ ...data, agreement: { ...data.agreement, multi_labelled: 0, cohen: [], judge: null } }} />);
		expect(none).toContain('data-testid="annotation-agreement-none"');
	});

	it("lets only managers change settings", () => {
		expect(render(<SettingsTab queue={queue} />)).toContain('data-testid="annotation-settings-save"');
		const ro = render(<SettingsTab queue={{ ...queue, can_manage: false }} />);
		expect(ro).not.toContain('data-testid="annotation-settings-save"');
		expect(text(ro)).toContain("Only the queue&#x27;s creator");
	});

	it("gives a manager of an empty queue a way to add runs, and others who to ask", () => {
		const empty = { ...queue, counts: { ...queue.counts, total: 0 } };
		const html = render(<Labeller queue={empty} />);
		expect(html).toContain('data-testid="annotation-label-add-runs"');
		expect(html).toContain('href="/annotation/q_1?tab=items"');
		const other = render(<Labeller queue={{ ...empty, can_manage: false }} />);
		expect(other).not.toContain('data-testid="annotation-label-add-runs"');
		expect(text(other)).toContain("Ask the queue&#x27;s owner to add runs.");
	});

	const exp = (over: Partial<ExperimentStatus>): ExperimentStatus => ({
		exp_id: "e_1",
		name: "e_1",
		state: "done",
		manifest_hash: "h",
		frozen_at: "2026-11-20T10:00:00Z",
		slots: { total: 1, done: 1, running: 0, failed: 0 },
		spent_usd: 0 as unknown as ExperimentStatus["spent_usd"],
		suite: "support-faq@1.0.0",
		...over,
	});

	it("offers only finished runs of the queue's benchmark, newest first", () => {
		const list = [
			exp({ exp_id: "e_old", name: "Old support run", finished_at: "2026-11-18T10:00:00Z" }),
			exp({ exp_id: "e_new", name: "New support run", finished_at: "2026-11-21T10:00:00Z" }),
			exp({ exp_id: "e_other", name: "Coding run", suite: "coding-core@1.0.0", finished_at: "2026-11-21T11:00:00Z" }),
			exp({ exp_id: "e_running", name: "Running support run", state: "running" }),
		];
		expect(finishedRunsOf(list, "support-faq").map((e) => e.exp_id)).toEqual(["e_new", "e_old"]);
		expect(finishedRunsOf(list, "").map((e) => e.exp_id)).toEqual(["e_other", "e_new", "e_old"]);
		exps.list = list;
		const html = render(<AddItems queue={queue} />);
		expect(html).toContain('data-testid="annotation-add-experiment"');
		expect(html).toContain('value="e_new"');
		expect(html).toContain('value="e_old"');
		expect(html).not.toContain('value="e_other"');
		expect(html).not.toContain('value="e_running"');
		expect(text(html)).toContain("New support run");
		expect(html).not.toContain("<input placeholder");
	});

	it("posts the chosen experiment with the sample size and balance", () => {
		expect(addItemsBody("q_1", "e_new", 12.7, true)).toEqual({ id: "q_1", experiment: "e_new", sample: 12, stratify: true });
		expect(addItemsBody("q_1", "e_new", -3, false)).toEqual({ id: "q_1", experiment: "e_new", sample: 0, stratify: false });
	});

	it("says how to get runs when the benchmark has none", () => {
		exps.list = [exp({ exp_id: "e_other", suite: "coding-core@1.0.0" })];
		const html = render(<AddItems queue={queue} />);
		expect(text(html)).toContain("No finished runs of support-faq yet. Run an evaluation on it first.");
		expect(html).toContain('href="/evaluate?suite=support-faq"');
		expect(html).not.toContain('data-testid="annotation-add-experiment"');
	});
});
