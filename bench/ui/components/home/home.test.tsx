import "@/lib/i18n";

import type { ExperimentList, ExperimentStatus, LiveEvent, SuiteList } from "@/lib/types/bench";
import { errorQuery, loadingQuery, okQuery, render, tagOf } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	byNewest,
	evaluationStats,
	homeStats,
	newestEvaluations,
	humanExperimentName,
	isSystemCheck,
	mergeEvents,
	recentComparisons,
	runningNow,
	splitExperiments,
	suiteAdmission,
	suiteReady,
} from "./home.utils";
import { RunningNowList } from "@/app/index/views/homeView";
import { RecentEvaluations, evaluationWho } from "./recentEvaluations";
import { RecentExperiments } from "./recentExperiments";
import { RunningCardBody, RunningExperiments } from "./runningExperiments";
import { SuitesPanel } from "./suitesPanel";

const api = vi.hoisted(() => ({ verdict: {} as unknown, verdictArgs: [] as unknown[], health: undefined as unknown }));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetHealthQuery: () => ({ data: api.health }),
}));
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetVerdictQuery: (arg: unknown, opts: { skip?: boolean }) => {
		api.verdictArgs.push({ arg, skip: opts?.skip });
		return opts?.skip ? { data: undefined, isLoading: false } : api.verdict;
	},
}));
// The running card polls; its body is tested directly, the list only for its empty state.
vi.mock("./useExperimentEvents", () => ({
	useExperimentEvents: (initial: ExperimentStatus) => ({ status: initial, events: [], error: undefined }),
}));

const fixtures = path.resolve(__dirname, "../../public/fixtures");
const read = <T,>(file: string): T => JSON.parse(fs.readFileSync(path.join(fixtures, file), "utf8")) as T;
const { experiments } = read<ExperimentList>("experiments.list.json");
const { suites } = read<SuiteList>("suites.json");
const events = read<{ events: LiveEvent[]; status: ExperimentStatus }>("experiments.events.e_synthetic-nightly.json");

describe("home utils", () => {
	it("splits running experiments from the newest finished ones", () => {
		const { running, recent } = splitExperiments(experiments);
		expect(running.map((exp) => exp.exp_id)).toEqual(["e_synthetic-nightly"]);
		expect(recent[0].exp_id).toBe("e_synthetic-codewins");
		expect(recent.map((exp) => exp.state)).not.toContain("running");
	});
	it("sorts by start time, else freeze time", () => {
		expect(byNewest(experiments).map((exp) => exp.exp_id)).toEqual([
			"e_synthetic-codewins",
			"e_synthetic-nightly",
			"e_synthetic-draft",
			"e_synthetic-matrix",
			"e_synthetic-outage",
		]);
	});
	it("merges events after the newest seq, in order, keeping the last ones", () => {
		const a = { seq: 1, kind: "slot_started", at: "" };
		const b = { seq: 2, kind: "stage", at: "" };
		const c = { seq: 3, kind: "slot_sealed", at: "" };
		expect(mergeEvents([a], [c, b, a]).map((e) => e.seq)).toEqual([1, 2, 3]);
		expect(mergeEvents([a, b], [c], 2).map((e) => e.seq)).toEqual([2, 3]);
	});
	it("reads the KPI row from experiments and runs", () => {
		const now = new Date("2026-11-21T12:00:00Z");
		const run = (started: string, outcome: string, sealed = true) =>
			({ started, outcome, sealed, counted: true }) as unknown as import("@/lib/types/bench").RunListRow;
		const stats = homeStats(
			[
				{ ...experiments[0], state: "running", started_at: "2026-11-20T00:00:00Z", spent_usd: { state: "known", v: 2.5 } },
				{ ...experiments[1], state: "done", started_at: "2026-10-01T00:00:00Z", spent_usd: { state: "known", v: 9 } },
			] as never,
			[
				run("2026-11-20T00:00:00Z", "pass"),
				run("2026-11-19T00:00:00Z", "fail"),
				run("2026-10-01T00:00:00Z", "pass"),
				run("2026-11-20T00:00:00Z", "pass", false),
			],
			now,
		);
		expect(stats).toMatchObject({ experiments: 2, running: 1, runsWeek: 3, graded: 3, passed: 2, spendMonth: 2.5 });
		expect(stats.passRate).toBeCloseTo(2 / 3);
		expect(homeStats([], [], now).passRate).toBeNull();
	});
	it("reads a suite's admission state", () => {
		const core = suites.find((suite) => suite.id === "coding-core")!;
		expect(suiteAdmission(core)).toMatchObject({ admitted: 4, total: 4, tone: "good" });
		const stale = suites.find((suite) => suite.id === "acme-payments")!;
		expect(suiteAdmission(stale).tone).toBe("warn");
		const broken = suites.find((suite) => suite.id === "acme-broken")!;
		expect(suiteAdmission(broken).tone).toBe("bad");
		expect(suiteReady(core)).toBe(true);
		expect(suiteReady(stale)).toBe(false);
	});
});

describe("RunningCardBody", () => {
	it("shows the slot bar, spend and the newest event with its run as a link", () => {
		const html = render(<RunningCardBody status={events.status} events={events.events} />);
		expect(tagOf(html, "home-running-e_synthetic-nightly")).toContain('data-state="running"');
		expect(html).toContain('role="progressbar"');
		expect(html).toContain("22 of 36 runs");
		expect(html).toContain("61%");
		expect(html).toContain('href="/experiments/e_synthetic-nightly"');
		expect(tagOf(html, "home-running-watch-e_synthetic-nightly")).toContain('href="/experiments/e_synthetic-nightly"');
		expect(html).toContain("animate-ping");
		expect(html).toContain("Saved");
		expect(html).toContain('href="/runs/r_synthetic-nightly-a-t2-1"');
	});
	it("waits for the first event, and says when it cannot follow", () => {
		expect(render(<RunningCardBody status={events.status} events={[]} />)).toContain("Getting started, no updates yet");
		expect(render(<RunningCardBody status={events.status} events={[]} error={{ status: 500 }} />)).toContain(
			"Lost the live updates. Trying again.",
		);
	});
	it("says nothing is running when the list is empty", () => {
		expect(render(<RunningExperiments experiments={[]} />)).toContain("Nothing is running right now.");
	});
});

describe("RecentExperiments", () => {
	beforeEach(() => {
		api.verdictArgs = [];
		api.verdict = okQuery(read("experiments.verdict.json"));
	});
	it("links each experiment and its suite, with state and the verdict decision", () => {
		const { recent } = splitExperiments(experiments);
		const html = render(<RecentExperiments experiments={recent} />);
		expect(html).toContain('href="/experiments/e_synthetic-codewins"');
		const verdict = read<{ suite: string; verdict: { decision: string } }>("experiments.verdict.json");
		expect(html).toContain(`data-decision="${verdict.verdict.decision}"`);
		expect(html).toContain(verdict.suite.split("@")[0]);
		// Only the finished experiment with an analysis asks for its verdict.
		expect(api.verdictArgs.filter((call) => !(call as { skip: boolean }).skip)).toHaveLength(1);
	});
	it("uses the list's own suite and decision when it carries them", () => {
		const exp = { ...experiments[0], suite: "coding-core", decision: "regressed" };
		const html = render(<RecentExperiments experiments={[exp]} />);
		expect(html).toContain('data-decision="regressed"');
		expect(api.verdictArgs.every((call) => (call as { skip: boolean }).skip)).toBe(true);
	});
	it("says the verdict is unavailable when its request failed", () => {
		api.verdict = errorQuery(500, "analysis failed");
		const html = render(<RecentExperiments experiments={[experiments[0]]} />);
		expect(html).toContain("Result not available");
		expect(html).toContain('title="analysis failed"');
	});
	it("shows a placeholder while the verdict loads", () => {
		api.verdict = loadingQuery();
		expect(render(<RecentExperiments experiments={[experiments[0]]} />)).toContain("animate-pulse");
	});
});

describe("SuitesPanel", () => {
	it("shows benchmarks as rows by human name with a Ready or Not ready yet dot", () => {
		const html = render(<SuitesPanel suites={suites} limit={20} />);
		expect(html).toContain('href="/suites/coding-core"');
		expect(tagOf(html, "home-suite-coding-core")).toContain('data-ready="ready"');
		expect(tagOf(html, "home-suite-acme-payments")).toContain('data-ready="attention"');
		expect(tagOf(html, "home-suite-acme-broken")).toContain('data-ready="failed"');
		expect(html).toContain("4 tasks");
		expect(html).toContain("Coding basics");
		expect(html).toContain("1 of 3 tasks ready");
		expect(html).not.toContain("admitted");
	});
	it("says how many tasks are ready, and Not ready yet only when none is", () => {
		const base = suites.find((suite) => suite.id === "coding-core")!;
		const html = render(
			<SuitesPanel
				suites={[
					{ ...base, id: "hd-harbor-tb2", task_count: 89, admitted_count: 24, lock_state: "locked" },
					{ ...base, id: "hd-swe-verified", task_count: 500, admitted_count: 0, lock_state: "locked" },
				]}
				limit={20}
			/>,
		);
		expect(tagOf(html, "home-suite-state-hd-harbor-tb2")).toContain('data-readiness="needsAdmission"');
		expect(html).toContain("24 of 89 tasks ready");
		expect(tagOf(html, "home-suite-state-hd-swe-verified")).toContain('data-readiness="notReady"');
		expect(html).toContain("Not ready yet");
	});
});

describe("Running now and Recent comparisons", () => {
	const ev = (id: string, created_at: string, status: string) =>
		({
			id,
			created_at,
			status,
			spec: {
				name: `eval ${id}`,
				mode: "score",
				arms: [{ label: "A", agent: "codex", model: "gpt-x" }],
				benchmarks: ["tau2"],
				tasks_per_benchmark: 5,
				trials: 1,
				faults: ["clean"],
			},
			children: [],
		}) as unknown as import("@/lib/store/apis/evaluationsApi").Evaluation;
	const exp = (
		exp_id: string,
		name: string,
		state: ExperimentStatus["state"],
		extra: Partial<ExperimentStatus> = {},
	): ExperimentStatus => ({
		exp_id,
		name,
		state,
		manifest_hash: "h",
		frozen_at: "2026-11-22T10:00:00Z",
		started_at: "2026-11-22T10:00:00Z",
		slots: { total: 4, done: 1, running: 1, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
		suite: "tb2@1.0.0",
		arms: ["A", "B"],
		...extra,
	});
	const evaluations = [
		ev("ev_1", "2026-11-22T10:00:00Z", "running"),
		ev("ev_2", "2026-11-22T11:00:00Z", "running"),
		ev("ev_3", "2026-11-22T12:00:00Z", "queued"),
		ev("ev_4", "2026-11-22T13:00:00Z", "running"),
		ev("ev_20261122_142833_4eaebd", "2026-11-22T14:28:33Z", "running"),
		ev("ev_old", "2026-11-21T09:00:00Z", "done"),
	];
	const experimentsNow = [
		exp("e_admit_acme_voice_desk_1a2b", "admit-acme-voice-desk", "running", { suite: "acme-voice-desk@1.0.0", arms: ["oracle", "noop"] }),
		exp("e_child", "codex score tau2", "running", { arms: ["A"], evaluation_id: "ev_4" }),
		// A quick run on the practice benchmark (generated name); a named comparison of two setups there is listed (N202).
		exp("e_practice", "quick-20261122-100000-ab12", "running", { suite: "coding-core@1.0.0" }),
		exp("e_mine", "prompt v2 vs v1", "running"),
	];
	it("lists every active evaluation, then running comparisons, cut to 4 after filtering", () => {
		const all = runningNow(evaluations, experimentsNow);
		expect(all.total).toBe(6);
		expect(all.evaluations.map((e) => e.id)).toEqual(["ev_20261122_142833_4eaebd", "ev_4", "ev_3", "ev_2"]);
		expect(all.comparisons).toEqual([]);
		expect(all.more).toBe(2);
		const few = runningNow(evaluations.slice(0, 2), experimentsNow);
		expect(few.comparisons.map((e) => e.exp_id)).toEqual(["e_mine"]);
		expect(few.more).toBe(0);
		expect(evaluationStats(evaluations).active).toBe(5);
	});
	it("shows 4 of 5 active evaluations with +1 more", () => {
		const running = runningNow(evaluations, []);
		expect(running.total).toBe(5);
		const html = render(<RunningNowList running={running} />);
		expect(html).toContain('href="/evaluations/ev_20261122_142833_4eaebd"');
		expect(html).not.toContain('href="/evaluations/ev_1"');
		expect(tagOf(html, "home-running-more")).toContain('href="/evaluations?status=running"');
		expect(html).toContain("+1 more");
	});
	it("keeps readiness checks, practice runs and evaluation benchmarks out of Recent comparisons", () => {
		const done = experimentsNow.map((e) => ({ ...e, state: "done" as const }));
		expect(recentComparisons(done).map((e) => e.exp_id)).toEqual(["e_mine"]);
		const html = render(<RecentExperiments experiments={recentComparisons(done)} />);
		expect(html).not.toContain("Readiness check");
		expect(html).not.toContain("acme-voice-desk");
	});
});

describe("humanExperimentName", () => {
	const labels = { check: "Readiness check", untitled: "Untitled comparison" };
	it("labels system checks and spaces out internal names", () => {
		expect(isSystemCheck({ name: "admit-coding-core", exp_id: "e_x" })).toBe(true);
		expect(isSystemCheck({ name: "nightly", exp_id: "e_admit_acme_5e8q" })).toBe(true);
		expect(isSystemCheck({ name: "nightly", exp_id: "e_nightly" })).toBe(false);
		expect(humanExperimentName({ name: "admit-coding-core", exp_id: "e_x" }, labels)).toBe("Readiness check: coding-core");
		expect(humanExperimentName({ name: "codex-vs-claude_nightly", exp_id: "e_x" }, labels)).toBe("Codex vs claude nightly");
		expect(humanExperimentName({ name: "", exp_id: "e_x" }, labels)).toBe("Untitled comparison");
	});
});

describe("RecentEvaluations", () => {
	const ev = (id: string, created_at: string, status: string, arms: { label: string; agent: string; version?: number; model: string }[]) =>
		({
			id,
			created_at,
			status,
			spec: {
				name: `eval ${id}`,
				mode: arms.length > 1 ? "versions" : "score",
				arms,
				benchmarks: ["hd-harbor-tb2", "tau2"],
				tasks_per_benchmark: 5,
				trials: 2,
				faults: ["clean"],
			},
			children: [],
		}) as unknown as import("@/lib/store/apis/evaluationsApi").Evaluation;
	const list = [
		ev("ev_1", "2026-11-01T00:00:00Z", "done", [{ label: "A", agent: "codex", model: "gpt-x" }]),
		ev("ev_2", "2026-11-20T00:00:00Z", "running", [
			{ label: "A", agent: "mybot", version: 1, model: "m" },
			{ label: "B", agent: "mybot", version: 2, model: "m" },
		]),
	];
	it("counts agents and evaluations, newest first", () => {
		expect(evaluationStats(list)).toEqual({ evaluations: 2, active: 1, agents: 2 });
		expect(newestEvaluations(list).map((e) => e.id)).toEqual(["ev_2", "ev_1"]);
		expect(newestEvaluations(list, 1)).toHaveLength(1);
	});
	it("links each evaluation with who it tested and its status", () => {
		expect(evaluationWho(list[1])).toBe("mybot v1 · m vs mybot v2 · m");
		const html = render(<RecentEvaluations evaluations={list} />);
		expect(html).toContain('href="/evaluations/ev_1"');
		expect(html).toContain("2 benchmarks");
		expect(tagOf(html, "home-evaluation-status-ev_2")).toContain('data-status="running"');
		// The shared status pill: the one status word for running.
		expect(tagOf(html, "home-evaluation-status-ev_2")).toContain('data-slot="status-pill"');
	});
	it("hides the project's own test evaluations (M1-18)", () => {
		const test = ev("ev_t", "2026-11-21T00:00:00Z", "done", [{ label: "A", agent: "zz-test-loop", model: "m" }]);
		const html = render(<RecentEvaluations evaluations={[...list, test]} />);
		expect(html).not.toContain('data-testid="home-evaluation-ev_t"');
		expect(html).toContain('data-testid="home-evaluation-ev_1"');
	});
});