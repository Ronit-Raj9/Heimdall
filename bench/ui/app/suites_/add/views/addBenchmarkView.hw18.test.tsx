import i18n from "@/lib/i18n";

import { DraftPreview, PublishPanel, publishQuestion } from "@/components/onboard/draftPanel";
import { InventoryPanel } from "@/components/onboard/inventoryPanel";
import type { OnboardDraftSet, OnboardInventory } from "@/lib/types/onboard";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AddBenchmarkView from "./addBenchmarkView";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

// ConfirmButton asks on its first click; the tests read what it asks and run its confirm.
const asked = vi.hoisted(() => ({ list: [] as { question: string; testId?: string; onConfirm(): void }[] }));
vi.mock("@/components/platform/badges", async (orig) => ({
	...(await orig<typeof import("@/components/platform/badges")>()),
	ConfirmButton: (p: { label: string; question: string; testId?: string; onConfirm(): void }) => {
		asked.list.push(p);
		return <button data-testid={p.testId}>{p.label}</button>;
	},
}));

const api = vi.hoisted(() => ({
	remove: vi.fn(),
	edit: vi.fn(),
	deleteError: undefined as unknown,
}));
vi.mock("@/lib/store/apis/onboardApi", () => {
	const idle = () => [() => undefined, { isLoading: false, error: undefined }];
	return {
		useUploadOnboardFileMutation: idle,
		useDetectOnboardMutation: idle,
		useInspectOnboardMutation: idle,
		useCreateOnboardDraftMutation: idle,
		useDeleteOnboardDraftMutation: () => [api.remove, { isLoading: false, error: api.deleteError }],
		useEditOnboardTaskMutation: () => [api.edit, { isLoading: false, error: undefined }],
		useStartOnboardReadinessMutation: idle,
		usePublishOnboardDraftMutation: idle,
		useListOnboardDraftsQuery: () => ({
			data: {
				drafts: [
					{ id: "d1", suite: "faq", title: "faq", kind: "rows", tasks: 2, ready: 1, published: false, updated: "", input: "faq.csv" },
				],
			},
		}),
		useGetOnboardDraftQuery: () => ({ data: undefined }),
	};
});
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuitesLightQuery: () => ({ data: { mode: "live", suites: [] } }) }));

const tx = (k: string, o?: Record<string, unknown>) => i18n.t(k as never, { ns: "onboard", ...o } as never) as unknown as string;

const set: OnboardDraftSet = {
	id: "20261122-204611-197a49",
	suite: "zz-test-csv",
	new_suite: true,
	title: "zz-test-csv",
	kind: "rows",
	source: "rows",
	input: { upload: "onboard/x/faq.csv" },
	plan: { source: "rows", suite: "zz-test-csv", held_out: { pct: 25, seed: 1 } },
	grader: "answer_match",
	public: false,
	problems: [],
	tasks: [
		{
			id: "what-is-2-2",
			origin: { source: "faq.csv", ref: "row 2" },
			title: "What is 2+2?",
			instruction: "What is 2+2?",
			checks: [],
			controls: [],
			gates: [],
			held_out: true,
			review: "accepted",
			readiness: { admitted: true, checks: [] },
		},
		{
			id: "capital-of-france",
			origin: { source: "faq.csv", ref: "row 3" },
			title: "Capital of France",
			instruction: "Capital of France?",
			checks: [],
			controls: [],
			gates: [],
			held_out: false,
			review: "accepted",
			readiness: { admitted: true, checks: [] },
		},
	],
};

beforeEach(() => {
	asked.list = [];
	api.remove.mockReset();
	api.edit.mockReset();
	api.deleteError = undefined;
});

describe("Earlier drafts Delete (N289)", () => {
	it("asks first, naming the benchmark and its drafted tasks, and deletes on confirm", () => {
		renderToStaticMarkup(<AddBenchmarkView />);
		const del = asked.list.find((p) => p.testId === "onboard-draft-delete-d1");
		expect(del?.question).toBe("Delete the draft for faq? Its 2 drafted task(s) go. A benchmark it already published stays.");
		expect(api.remove).not.toHaveBeenCalled();
		del?.onConfirm();
		expect(api.remove).toHaveBeenCalledWith("d1");
	});

	it("shows a failed delete", () => {
		api.deleteError = { status: 500, data: { error: { message: "disk full" } } };
		const html = renderToStaticMarkup(<AddBenchmarkView />);
		expect(html).toContain("Could not delete the draft");
		expect(html).toContain("disk full");
	});
});

describe("Draft preview (N235, draft task Drop)", () => {
	it("titles a new benchmark's draft in one sentence", () => {
		const html = renderToStaticMarkup(<DraftPreview set={set} />);
		expect(html).toContain("2 tasks in draft 20261122-204611-197a49 for zz-test-csv (new benchmark)");
		expect(html).not.toContain(">new benchmark<");
	});

	it("asks before dropping a task", () => {
		renderToStaticMarkup(<DraftPreview set={set} />);
		const drop = asked.list.find((p) => p.testId === "onboard-drop-what-is-2-2");
		expect(drop?.question).toBe("Drop What is 2+2? from this draft? It cannot be added back without drafting again.");
		expect(api.edit).not.toHaveBeenCalled();
		drop?.onConfirm();
		expect(api.edit).toHaveBeenCalledWith({ set: set.id, task: "what-is-2-2", drop: true });
	});
});

describe("Held out made visible (N252)", () => {
	it("names the held-out count in the publish confirm", () => {
		expect(publishQuestion(set, false, tx)).toBe("Add 2 tasks to zz-test-csv? 1 of them are held out (private, never in shared results).");
		const none = { ...set, tasks: set.tasks.map((t) => ({ ...t, held_out: false })) };
		expect(publishQuestion(none, false, tx)).toBe("Add 2 tasks to zz-test-csv?");
	});

	it("says what the plan holds out and offers the readiness checks after publish", () => {
		const html = renderToStaticMarkup(
			<PublishPanel
				set={{
					...set,
					published: { suite: "zz-test-csv", tasks: ["what-is-2-2"], created: true, held_out: 1, recheck: ["what-is-2-2"], job_id: "j" },
				}}
			/>,
		);
		expect(html).toContain("1 held out (the plan holds out 25%).");
		expect(html).toContain("Run the readiness checks now");
		expect(html).toContain('data-testid="onboard-run-readiness"');
	});

	it("shows the held-out share in the plan as a sentence", () => {
		const inv: OnboardInventory = {
			kind: "rows",
			source: "rows",
			title: "faq.csv",
			license: "your data",
			unit: "row",
			units: 3,
			columns: ["question", "answer"],
			samples: [{ ref: "row 2", fields: { question: "q", answer: "a" } }],
			suggest: { source: "rows", suite: "faq", held_out: { pct: 25, seed: 1 } },
			estimate: { tasks: 3, readiness_runs: 18, judge_calls: 0, assist_usd: 0 },
			can_draft: true,
			next: "Check the plan, then Draft",
		};
		const html = renderToStaticMarkup(
			<InventoryPanel inv={inv} plan={inv.suggest} onPlan={() => undefined} onDraft={() => undefined} busy={false} />,
		);
		expect(html).toContain("Held out: 25% of the tasks (1 of 3) stay private. Change it here.");
	});
});