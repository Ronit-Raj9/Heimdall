import "@/lib/i18n";

import { cellState, checkGroup, DraftPreview, matrixColumns, PublishPanel, ReadinessPanel } from "@/components/onboard/draftPanel";
import { InventoryPanel, onboardColumns, onboardMapping } from "@/components/onboard/inventoryPanel";
import { SourceBox, sourceGiven } from "@/components/onboard/sourceBox";
import { SUITE_ID_RE, suggestSuiteId, suiteIdCheck } from "@/components/onboard/onboard.utils";
import type { OnboardDraftSet, OnboardInventory } from "@/lib/types/onboard";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import AddBenchmarkView from "./addBenchmarkView";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
	useBlocker: () => undefined,
}));

const { idleMutation } = vi.hoisted(() => ({ idleMutation: () => [() => undefined, { isLoading: false, error: undefined }] }));
vi.mock("@/lib/store/apis/onboardApi", () => ({
	useUploadOnboardFileMutation: idleMutation,
	useDetectOnboardMutation: idleMutation,
	useInspectOnboardMutation: idleMutation,
	useCreateOnboardDraftMutation: idleMutation,
	useDeleteOnboardDraftMutation: idleMutation,
	useEditOnboardTaskMutation: idleMutation,
	useStartOnboardReadinessMutation: idleMutation,
	usePublishOnboardDraftMutation: idleMutation,
	useListOnboardDraftsQuery: () => ({ data: { drafts: [] } }),
	useGetOnboardDraftQuery: () => ({ data: undefined }),
}));

// N46: the id checks read the light suite list.
const lightSuites = vi.hoisted(() => ({
	suites: [
		{ id: "coding-core", version: "1.0.0", builtin: true, source: "library", task_count: 40 },
		{ id: "faq", version: "1.0.0", task_count: 4 },
	] as unknown[],
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({ data: { mode: "live", suites: lightSuites.suites } }),
}));

const pass = (name: string, want = "pass") => ({ name, want, got: want, passed: true });
const set: OnboardDraftSet = {
	id: "20261122-101500-ab12cd",
	suite: "faq",
	new_suite: true,
	title: "faq",
	kind: "rows",
	source: "rows",
	input: { upload: "onboard/x/faq.csv" },
	plan: { source: "rows", suite: "faq", held_out: { pct: 25, seed: 1 } },
	grader: "answer_match",
	public: false,
	problems: [{ ref: "row 7", message: "no check: give an answer" }],
	readiness: { status: "done", where: "this server", done: 2, total: 2, admitted: 1, refused: 1 },
	tasks: [
		{
			id: "hotel-cap",
			origin: { source: "faq.csv", ref: "row 2" },
			title: "What is the hotel cap?",
			instruction: "# What is the hotel cap?",
			checks: ['The final answer must equal "250" (case and spacing ignored).'],
			controls: [
				{ kind: "reference", path: "solution/answer.txt", origin: "source" },
				{ kind: "wrong_answer", path: "controls/mutant-1.txt", origin: "source" },
			],
			gates: [{ gate: "static", ok: true, blocking: true }],
			held_out: true,
			review: "accepted",
			readiness: {
				admitted: true,
				checks: [
					pass("reference_1"),
					pass("reference_2"),
					pass("empty_control", "fail"),
					pass("wrong_answer_1", "fail"),
					pass("exploit", "fail"),
				],
			},
			catch_rate: { caught: 3, total: 3 },
		},
		{
			id: "days",
			origin: { source: "faq.csv", ref: "row 3" },
			title: "Vacation days",
			instruction: "# Vacation days",
			checks: ['The final answer must equal "20".'],
			controls: [],
			gates: [{ gate: "duplicate", ok: false, blocking: false, message: "near copy of task x" }],
			held_out: false,
			review: "accepted",
			readiness: { admitted: false, checks: [pass("reference_1"), { name: "exploit", want: "fail", got: "pass", passed: false }] },
			catch_rate: { caught: 0, total: 1 },
			reason: "failed: exploit",
		},
	],
};

describe("Add benchmark", () => {
	it("renders the drop box first", () => {
		const html = renderToStaticMarkup(<NuqsTestingAdapter><AddBenchmarkView /></NuqsTestingAdapter>);
		expect(html).toContain('data-testid="onboard-source"');
		expect(html).toContain('data-testid="onboard-drop"');
		expect(html).not.toContain("\u2014");
	});

	it("groups readiness checks into matrix columns", () => {
		expect(checkGroup("reference_3")).toBe("reference");
		expect(checkGroup("oracle_control")).toBe("reference");
		expect(checkGroup("mutant_1")).toBe("wrong");
		expect(checkGroup("empty_patch")).toBe("empty");
		expect(matrixColumns(set.tasks)).toEqual(["reference", "empty", "wrong", "exploit"]);
		expect(cellState(set.tasks[1].readiness?.checks, "exploit")).toBe("fail");
		expect(cellState(set.tasks[1].readiness?.checks, "empty")).toBe("none");
	});

	it("shows the matrix with catch rates and reasons", () => {
		const html = renderToStaticMarkup(<ReadinessPanel set={set} />);
		expect(html).toContain('data-testid="onboard-matrix"');
		expect(html).toContain("3/3");
		expect(html).toContain("failed: exploit");
		expect(html).toContain("not run");
	});

	it("previews tasks with gates and problems", () => {
		const html = renderToStaticMarkup(<DraftPreview set={set} />);
		expect(html).toContain('data-testid="onboard-task-hotel-cap"');
		expect(html).toContain("Duplicate");
		expect(html).toContain("row 7");
	});

	it("publishes only ready, accepted tasks by default", () => {
		const html = renderToStaticMarkup(<PublishPanel set={set} />);
		expect(html).toContain("1 ready and accepted of 2");
		const done = renderToStaticMarkup(
			<PublishPanel set={{ ...set, published: { suite: "faq", tasks: ["hotel-cap"], created: true, held_out: 1, job_id: "j" } }} />,
		);
		expect(done).toContain('data-testid="onboard-evaluate"');
	});

	it("lets rows be mapped when nothing could be read yet", () => {
		const inv: OnboardInventory = {
			kind: "rows",
			source: "rows",
			title: "ls",
			license: "your data",
			unit: "row",
			units: 2,
			columns: ["inputs.q", "outputs.gold"],
			samples: [],
			suggest: { source: "rows", suite: "ls", held_out: { pct: 25, seed: 1 } },
			estimate: { tasks: 0, readiness_runs: 0, judge_calls: 0, assist_usd: 0 },
			can_draft: false,
			next: "map the question and answer columns",
		};
		const html = renderToStaticMarkup(
			<InventoryPanel inv={inv} plan={inv.suggest} onPlan={() => undefined} onDraft={() => undefined} busy={false} />,
		);
		expect(html).toContain('data-testid="onboard-plan-mapping"');
		expect(html).toContain("outputs.gold");
		// The Build tab's row mapper, limited to the fields onboard reads.
		expect(html).toContain('data-testid="suite-build-map-reference"');
		expect(html).not.toContain('data-testid="suite-build-map-messages"');
		expect(html).not.toContain('data-testid="suite-build-map-save"');
	});

	it("keeps only onboard fields that are set in the plan mapping", () => {
		expect(onboardMapping({ question: "inputs.q", expected: "", messages: "m", split: "-" })).toEqual({ question: "inputs.q", split: "-" });
		const cols = onboardColumns({ columns: ["q"], samples: [{ ref: "row 1", fields: { q: "hi" } }] });
		expect(cols).toEqual([{ path: "q", kind: "text", example: "hi", filled: 1 }]);
	});
});

// C5.empty: Look inside cannot be pressed with nothing given, and the hint says what to give before
// any click, so an empty submit never looks like it did nothing.
describe("Add benchmark source box", () => {
	it("disables Look inside until a link, a file or pasted rows are given, with the hint always shown", () => {
		const html = renderToStaticMarkup(<SourceBox busy={false} onSubmit={() => undefined} onClear={() => undefined} />);
		expect(html).toMatch(
			/<button[^>]*disabled=""[^>]*data-testid="onboard-inspect"|<button[^>]*data-testid="onboard-inspect"[^>]*disabled=""/,
		);
		expect(html).toContain('data-testid="onboard-source-hint"');
		expect(html).toContain("Give a link, a file or pasted rows.");
	});
	it("counts a link, a file or pasted rows as input, and blank text as none", () => {
		expect(sourceGiven({ link: "", rows: "", file: null })).toBe(false);
		expect(sourceGiven({ link: "   ", rows: "\n ", file: null })).toBe(false);
		expect(sourceGiven({ link: "", rows: "question,answer\nA,B", file: null })).toBe(true);
		expect(sourceGiven({ link: "terminal-bench@2.0", rows: "", file: null })).toBe(true);
		expect(sourceGiven({ link: "", rows: "", file: new File(["x"], "a.csv") })).toBe(true);
	});
});
// N46 and N47: the Benchmark id is checked as it is typed, and the estimate says what readiness costs.
describe("Add benchmark plan checks", () => {
	const inv: OnboardInventory = {
		kind: "rows",
		source: "rows",
		title: "faq.csv",
		license: "your data",
		unit: "row",
		units: 3,
		columns: ["question", "answer"],
		samples: [{ ref: "row 1", fields: { question: "q", answer: "a" } }],
		suggest: { source: "rows", suite: "faq", held_out: { pct: 25, seed: 1 } },
		estimate: { tasks: 3, readiness_runs: 18, judge_calls: 0, assist_usd: 0 },
		can_draft: true,
		next: "Check the plan, then Draft",
	};
	const panel = (suite: string, over: Partial<OnboardInventory> = {}) =>
		renderToStaticMarkup(
			<InventoryPanel
				inv={{ ...inv, ...over }}
				plan={{ ...inv.suggest, suite }}
				onPlan={() => undefined}
				onDraft={() => undefined}
				busy={false}
			/>,
		);
	const draftDisabled = (html: string) =>
		/<button[^>]*disabled=""[^>]*data-testid="onboard-draft"|<button[^>]*data-testid="onboard-draft"[^>]*disabled=""/.test(html);

	it("applies the server's id rule", () => {
		expect(SUITE_ID_RE.test("faq")).toBe(true);
		expect(SUITE_ID_RE.test("my-suite_1.2")).toBe(true);
		expect(SUITE_ID_RE.test("bad id!")).toBe(false);
		expect(SUITE_ID_RE.test("-x")).toBe(false);
		expect(SUITE_ID_RE.test("a".repeat(65))).toBe(false);
		const suites = lightSuites.suites as { id: string; builtin?: boolean; task_count: number }[];
		expect(suiteIdCheck("bad id!", suites)).toEqual({ state: "invalid" });
		expect(suiteIdCheck("coding-core", suites)).toEqual({ state: "builtin" });
		expect(suiteIdCheck("faq", suites)).toEqual({ state: "existing", count: 4 });
		expect(suiteIdCheck("new-one", suites)).toEqual({ state: "new" });
		expect(suiteIdCheck("", suites)).toEqual({ state: "empty" });
		expect(suggestSuiteId("My FAQ (2024).csv", suites)).toBe("my-faq-2024");
		expect(suggestSuiteId("coding-core", suites)).toBe("coding-core-own");
	});

	it("shows the rule and disables Draft for an invalid id", () => {
		const html = panel("bad id!");
		expect(html).toContain('data-testid="onboard-plan-suite-check"');
		expect(html).toContain("Use 1 to 64 lowercase letters");
		expect(draftDisabled(html)).toBe(true);
	});

	it("refuses a built-in benchmark id", () => {
		const html = panel("coding-core");
		expect(html).toContain("coding-core is the name of a built-in benchmark. Pick another id.");
		expect(draftDisabled(html)).toBe(true);
	});

	it("explains that an existing org benchmark gets the tasks added", () => {
		const html = panel("faq");
		expect(html).toContain("Adds tasks to your benchmark faq (4 tasks now).");
		expect(draftDisabled(html)).toBe(false);
		expect(panel("brand-new")).toContain("Creates a new private benchmark brand-new.");
	});

	it("says readiness runs cost nothing, and names judge calls only when there are any", () => {
		const html = panel("faq");
		expect(html).toContain("18 readiness runs");
		expect(html).toContain("cost nothing");
		expect(html).not.toContain("judge calls");
		const judged = panel("faq", { estimate: { tasks: 3, readiness_runs: 18, judge_calls: 18, assist_usd: 0 } });
		expect(judged).toContain("Rubric grading adds 18 judge calls, paid on your own model key.");
	});
});