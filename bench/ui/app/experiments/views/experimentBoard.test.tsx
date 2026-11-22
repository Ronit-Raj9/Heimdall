import "@/lib/i18n";

import type { ExperimentStatus } from "@/lib/types/bench";
import { okQuery, render, tagOf } from "@/lib/testing/render";
import { Code2, FlaskConical, SquareTerminal } from "lucide-react";
import { describe, expect, it, vi } from "vitest";
import { humanExperimentName, isAutoName } from "@/components/experiments/experiments.utils";
import { ExperimentBoard, suiteIcon, verdictTone } from "./experimentBoard";
import ExperimentListView from "./experimentListView";

const rows: ExperimentStatus[] = [
	{
		exp_id: "e_quick1",
		name: "quick-20261121-101500-ab12",
		state: "running",
		manifest_hash: "h",
		frozen_at: "2026-11-21T10:15:00Z",
		started_at: "2026-11-21T10:15:00Z",
		slots: { total: 8, done: 3, running: 2, failed: 0 },
		spent_usd: { state: "observed", v: 0.42 },
		suite: "swe-lite@1.0.0",
		arms: ["A", "B"],
	},
	{
		exp_id: "e_named",
		name: "Prompt v2 vs v1",
		state: "done",
		manifest_hash: "h",
		frozen_at: "2026-11-20T10:15:00Z",
		slots: { total: 4, done: 4, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 1.2 },
		suite: "terminal-ops@2.0.0",
		arms: ["A", "B"],
		decision: "improved",
	},
	{
		exp_id: "e_admit_acme_safety_5e8q",
		name: "admit-acme-safety",
		state: "done",
		manifest_hash: "h",
		frozen_at: "2026-11-19T10:15:00Z",
		slots: { total: 2, done: 2, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
		suite: "acme-safety@1.0.0",
		arms: ["oracle", "noop"],
	},
	{
		exp_id: "e_eval_child",
		name: "codex score tau2",
		state: "running",
		manifest_hash: "h",
		frozen_at: "2026-11-22T14:28:33Z",
		slots: { total: 10, done: 2, running: 1, failed: 0 },
		spent_usd: { state: "observed", v: 0.1 },
		suite: "tau2@1.0.0",
		arms: ["A"],
		evaluation_id: "ev_20261122_142833_4eaebd",
	},
	{
		exp_id: "e_practice",
		name: "my practice run",
		state: "done",
		manifest_hash: "h",
		frozen_at: "2026-11-18T10:15:00Z",
		slots: { total: 2, done: 2, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
		suite: "coding-core@1.0.0",
		arms: ["A", "B"],
	},
];
const url = vi.hoisted(() => ({
	all: null as string | null,
	q: null as string | null,
	state: null as string | null,
	extra: [] as import("@/lib/types/bench").ExperimentStatus[],
}));

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("nuqs", () => ({
	parseAsString: {},
	useQueryStates: () => [{ q: url.q, state: url.state, suite: null, all: url.all }, () => undefined],
}));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useListExperimentsQuery: () => okQuery({ experiments: [...rows, ...url.extra] }),
}));
vi.mock("@/lib/store/apis/evaluationsApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/evaluationsApi")>()),
	useListEvaluationsQuery: () => okQuery([{ id: "ev_two", spec: { name: "codex: two models" } }]),
}));

const t = (k: string, o?: Record<string, string>) => `${k}:${o?.arms ?? ""}:${o?.suite ?? ""}`;

describe("experiment list helpers", () => {
	it("builds a readable name for generated names only", () => {
		expect(isAutoName(rows[0])).toBe(true);
		expect(isAutoName(rows[1])).toBe(false);
		expect(humanExperimentName(rows[0], t)).toBe("quickRun::swe-lite");
		expect(humanExperimentName({ ...rows[0], arms: ["codex-gpt", "claude-code"] }, t)).toBe("armsVs:codex-gpt vs claude-code:swe-lite");
		expect(humanExperimentName(rows[1], t)).toBe("Prompt v2 vs v1");
		expect(humanExperimentName(rows[2], (k, o) => `${k}:${o?.name}`)).toBe("system.readiness:acme-safety");
	});
	it("maps verdicts to plain words and suites to icons", () => {
		expect(verdictTone("improved")).toEqual({ key: "improved", tone: "green" });
		expect(verdictTone("regressed").key).toBe("regressed");
		expect(verdictTone(undefined).key).toBe("pending");
		expect(suiteIcon("coding-core@1.0.0")).toBe(Code2);
		expect(suiteIcon("terminal-ops")).toBe(SquareTerminal);
		expect(suiteIcon("zzz")).toBe(FlaskConical);
	});
});

describe("ExperimentBoard", () => {
	it("renders readable names, status, verdict words, progress and the filter toolbar", () => {
		const html = render(<ExperimentBoard />);
		expect(html).toContain("Quick run on swe-lite");
		expect(html).toContain("Prompt v2 vs v1");
		expect(html).toContain("B is better");
		expect(html).toContain("3 of 8");
		expect(html).toContain('data-testid="experiment-filter-status"');
		expect(html).toContain('<option value="running">Running (1)</option>');
		expect(html).toContain('data-testid="experiment-filter-suite"');
		expect(html).toContain(">terminal-ops</option>");
		expect(html).not.toContain("experiment-chip-");
		expect(html).toContain("animate-ping");
		expect(html).not.toContain(">quick-20261121-101500-ab12<");
	});
	it("hides checks, practice runs and single-setup evaluation benchmarks unless all=1", () => {
		url.all = null;
		const html = render(<ExperimentBoard />);
		expect(html).not.toContain("experiment-row-e_admit_acme_safety_5e8q");
		expect(html).not.toContain("experiment-row-e_eval_child");
		expect(html).not.toContain("experiment-row-e_practice");
		expect(html).toContain("experiment-row-e_named");
		expect(html).toContain("Also show readiness checks, practice runs, debug runs and single benchmarks of evaluations (3)");
		expect(html).toContain("Showing 2 of 2");
		url.all = "1";
		const every = render(<ExperimentBoard />);
		url.all = null;
		expect(every).toContain("experiment-row-e_admit_acme_safety_5e8q");
		expect(every).toContain("experiment-row-e_eval_child");
		expect(every).toContain("experiment-row-e_practice");
		expect(tagOf(every, "experiment-show-system")).toContain("checked");
	});
	it("has a New comparison button and says where evaluation benchmarks live", () => {
		const html = render(<ExperimentListView />);
		expect(tagOf(html, "experiments-new")).toContain('href="/experiments/new"');
		expect(html).toContain("New comparison");
		expect(html).toContain("The single benchmarks of an evaluation are on its evaluation page.");
	});

	it("offers Cancelled, counts each status and puts analyzing under Running (N1)", () => {
		url.extra = [
			{ ...rows[1], exp_id: "e_cancel", name: "Stopped one", state: "cancelled", decision: undefined },
			{ ...rows[1], exp_id: "e_analyze", name: "Working one", state: "analyzing", decision: undefined },
		];
		const html = render(<ExperimentBoard />);
		expect(html).toContain('<option value="cancelled">Cancelled (1)</option>');
		expect(html).toContain('<option value="running">Running (2)</option>');
		expect(html).toContain('<option value="done">Finished (1)</option>');
		url.state = "cancelled";
		const cancelled = render(<ExperimentBoard />);
		expect(cancelled).toContain("experiment-row-e_cancel");
		expect(cancelled).not.toContain("experiment-row-e_named");
		url.state = "running";
		const running = render(<ExperimentBoard />);
		expect(running).toContain("experiment-row-e_analyze");
		expect(running).toContain("experiment-row-e_quick1");
		expect(running).not.toContain("experiment-row-e_cancel");
		url.state = null;
		url.extra = [];
	});
	it("names evaluation benchmarks after their evaluation and finds them by it (N2)", () => {
		url.extra = [
			{
				...rows[0],
				exp_id: "e_eval_two",
				name: "eval-20261122-143334-423797-hd-harbor-bfcl",
				suite: "hd-harbor-bfcl@1.0.0",
				evaluation_id: "ev_two",
				arms: ["A", "B"],
			},
		];
		url.q = "two models";
		const html = render(<ExperimentBoard />);
		url.q = null;
		url.extra = [];
		expect(html).toContain("experiment-row-e_eval_two");
		expect(html).toContain("codex: two models · BFCL function calling");
		expect(html).not.toContain("experiment-row-e_named");
		expect(html).toContain("Search by name, evaluation or benchmark");
	});
	it("hides debug runs by default and tags them when shown (N29)", () => {
		url.extra = [{ ...rows[1], exp_id: "e_replay_r_n697kiewjt_x4qq", name: "replay r_n697kiewjt", arms: ["A"], decision: undefined }];
		const html = render(<ExperimentBoard />);
		expect(html).not.toContain("experiment-row-e_replay_r_n697kiewjt_x4qq");
		expect(html).toContain("debug runs");
		url.all = "1";
		const every = render(<ExperimentBoard />);
		url.all = null;
		url.extra = [];
		expect(every).toContain("experiment-row-e_replay_r_n697kiewjt_x4qq");
		expect(every).toContain("Replay of run r_n697kiewjt");
		expect(every).toContain(" · debug run<");
	});
});