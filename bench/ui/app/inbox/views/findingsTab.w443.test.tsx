import "@/lib/i18n";

import { FindingDetail, reopenReason } from "@/components/findings/findingDetail";
import { investigationRows } from "@/components/findings/investigationList";
import type { Finding } from "@/lib/store/apis/findingsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import FindingsTab from "./findingsTab";

const state = vi.hoisted(() => ({ list: {} as Record<string, unknown> }));

vi.mock("nuqs", () => ({
	parseAsString: {},
	useQueryState: () => [null, () => Promise.resolve(new URLSearchParams())],
}));

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, params, hash }: { children: React.ReactNode; params: { runId: string }; hash?: string }) => (
		<a href={`/runs/${params.runId}${hash ? `#${hash}` : ""}`}>{children}</a>
	),
	// The "Show archived" switch reads and writes ?archived=1.
	useSearch: () => ({}),
	useNavigate: () => () => undefined,
}));

vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useListExperimentsQuery: () => ({ data: { experiments: [] } }),
	useGetExperimentQuery: () => ({ data: undefined }),
}));

const { mutation } = vi.hoisted(() => ({
	mutation: () => [() => Promise.resolve({}), { isLoading: false }],
}));

// The list reads through the archive-aware query (phase D decision 6).
vi.mock("@/lib/store/apis/archiveApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/archiveApi")>()),
	useGetFindingsWithArchivedQuery: () => state.list,
	useArchiveObjectMutation: mutation,
	useUnarchiveObjectMutation: mutation,
}));

vi.mock("@/lib/store/apis/findingsApi", () => ({
	useGetFindingsQuery: () => state.list,
	useFindingReasonMutation: mutation,
	useCheckFindingMutation: mutation,
	useRerunFindingMutation: mutation,
	useAddFindingMutation: mutation,
	useGetInvestigationsQuery: () => ({ data: [] }),
	useGetInvestigationQuery: () => ({ data: undefined }),
}));

vi.mock("@/lib/store/apis/jobRunApi", () => ({
	useCreateJobMutation: mutation,
}));

const base: Finding = {
	id: "fnd-000000000001",
	key: "investigation:job1:0",
	title: "Skips the tests",
	source: "investigation",
	origin: "model",
	state: "open",
	tasks: [{ suite: "coding-core", task: "t01" }],
	runs: ["r_t01_A"],
	hypothesis: "It never ran the tests",
	quotes: [{ run_id: "r_t01_A", seq: 4, text: "skip tests" }],
	history: [
		{
			at: "2026-11-01T10:00:00Z",
			by: "Ann",
			from: "",
			to: "open",
			reason: "Tracked as a finding.",
		},
	],
	created_at: "2026-11-01T10:00:00Z",
	updated_at: "2026-11-01T10:00:00Z",
};

describe("Findings tab", () => {
	beforeEach(() => {
		state.list = {
			isLoading: false,
			data: { mode: "live", findings: [], counts: {} },
		};
	});

	it("shows model text only as a hypothesis, with quotes that open the run at the step", () => {
		const html = renderToStaticMarkup(<FindingDetail finding={base} onClose={() => undefined} />);
		expect(html).toContain("Hypothesis written by a model. Quotes were checked against the recorded steps.");
		expect(html).toContain("It never ran the tests");
		expect(html).toContain('href="/runs/r_t01_A#step-4"');
		expect(html).toContain("Check the fix");
		expect(html).not.toContain("\u2014");
	});

	it("hides the possible cause for verified findings", () => {
		const html = renderToStaticMarkup(<FindingDetail finding={{ ...base, origin: "verified" }} onClose={() => undefined} />);
		expect(html).not.toContain("Possible cause");
	});

	it("shows the proof of a fixed finding and the reason of a reopened one", () => {
		const fixed: Finding = {
			...base,
			state: "fixed",
			fix: {
				experiment: "e2",
				agent: "bot",
				fixed_hash: "n",
				old_hash: "o",
				fixed_passed: 3,
				fixed_runs: 3,
				old_passed: 1,
				old_runs: 3,
				at: "",
			},
		};
		expect(renderToStaticMarkup(<FindingDetail finding={fixed} onClose={() => undefined} />)).toContain(
			"Fixed: the fixed agent passed 3 of 3 runs; the old agent passed 1 of 3.",
		);
		const reopened: Finding = {
			...base,
			state: "reopened",
			history: [
				...base.history,
				{
					at: "2026-11-02T10:00:00Z",
					by: "Heimdall",
					from: "fixed",
					to: "reopened",
					reason: "Task coding-core/t01 failed again in e3 (run r9)",
				},
			],
		};
		expect(reopenReason(reopened)).toBe("Task coding-core/t01 failed again in e3 (run r9)");
		expect(renderToStaticMarkup(<FindingDetail finding={reopened} onClose={() => undefined} />)).toContain("finding-reopened");
	});

	it("lists findings with their state badge, and says so when there are none", () => {
		expect(renderToStaticMarkup(<FindingsTab />)).toContain("No tracked failures yet");
		state.list = {
			isLoading: false,
			data: { mode: "live", findings: [base], counts: { open: 1 } },
		};
		const html = renderToStaticMarkup(<FindingsTab />);
		expect(html).toContain("Skips the tests");
		expect(html).toContain("finding-state-open");
		expect(html).toContain("Find out why");
	});

	it("shows the Show archived switch, and an archived finding with its pill and row menu", () => {
		state.list = {
			isLoading: false,
			data: { mode: "live", findings: [base, { ...base, id: "fnd-000000000002", archived_at: "2026-11-20T10:00:00Z" }], counts: { open: 2 } },
		};
		const html = renderToStaticMarkup(<FindingsTab />);
		expect(html).toContain('data-testid="findings-show-archived"');
		expect(html).toContain("Show archived");
		expect(html).toContain('data-testid="finding-archived-fnd-000000000002"');
		expect(html).not.toContain('data-testid="finding-archived-fnd-000000000001"');
	});

	it("reads the investigation list in either answer shape", () => {
		const row = {
			id: "j",
			exp_id: "e",
			created_at: "",
			runs: 1,
			items: 1,
			spent_usd: 0,
		};
		expect(investigationRows([row])).toHaveLength(1);
		expect(investigationRows({ mode: "live", investigations: [row] })).toHaveLength(1);
		expect(investigationRows(undefined)).toHaveLength(0);
	});
});