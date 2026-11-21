import "@/lib/i18n";

import type { ExperimentList, ExperimentStatus, LiveEvent, SuiteList } from "@/lib/types/bench";
import { demoSession } from "@/lib/testing/platformFixtures";
import { errorQuery, loadingQuery, okQuery, render, tagOf } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { byNewest, mergeEvents, splitExperiments, suiteAdmission } from "./home.utils";
import { GetStartedCard, QuickActions } from "./quickActions";
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
	it("reads a suite's admission state", () => {
		const core = suites.find((suite) => suite.id === "coding-core")!;
		expect(suiteAdmission(core)).toMatchObject({ admitted: 4, total: 4, tone: "good" });
		const stale = suites.find((suite) => suite.id === "acme-payments")!;
		expect(suiteAdmission(stale).tone).toBe("warn");
		const broken = suites.find((suite) => suite.id === "acme-broken")!;
		expect(suiteAdmission(broken).tone).toBe("bad");
	});
});

describe("RunningCardBody", () => {
	it("shows the slot bar, spend and the newest event with its run as a link", () => {
		const html = render(<RunningCardBody status={events.status} events={events.events} />);
		expect(tagOf(html, "home-running-e_synthetic-nightly")).toContain('data-state="running"');
		expect(html).toContain('role="progressbar"');
		expect(html).toContain("22 of 36 slots sealed");
		expect(html).toContain('href="/experiments/e_synthetic-nightly"');
		expect(html).toContain("Run sealed");
		expect(html).toContain('href="/runs/r_synthetic-nightly-a-t2-1"');
	});
	it("waits for the first event, and says when it cannot follow", () => {
		expect(render(<RunningCardBody status={events.status} events={[]} />)).toContain("Waiting for the first event.");
		expect(render(<RunningCardBody status={events.status} events={[]} error={{ status: 500 }} />)).toContain(
			"Could not follow this experiment.",
		);
	});
	it("says nothing is running when the list is empty", () => {
		expect(render(<RunningExperiments experiments={[]} />)).toContain("Nothing is running.");
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
		expect(tagOf(html, "home-recent-state-e_synthetic-codewins")).toContain('data-state="done"');
		const verdict = read<{ suite: string; verdict: { decision: string } }>("experiments.verdict.json");
		expect(html).toContain(`data-decision="${verdict.verdict.decision}"`);
		expect(html).toContain(`href="/suites/${verdict.suite.split("@")[0]}"`);
		expect(html).toContain(`>${verdict.suite.split("@")[0]}</a>`);
		expect(html).toContain(`title="Open suite ${verdict.suite}"`);
		expect(html).toContain("Not started");
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
		expect(html).toContain("Unavailable");
		expect(html).toContain('title="analysis failed"');
	});
	it("shows a placeholder while the verdict loads", () => {
		api.verdict = loadingQuery();
		expect(render(<RecentExperiments experiments={[experiments[0]]} />)).toContain("animate-pulse");
	});
});

describe("SuitesPanel", () => {
	it("lists suites with their lock and admitted counts as links", () => {
		const html = render(<SuitesPanel suites={suites} />);
		expect(html).toContain('href="/suites/coding-core"');
		expect(tagOf(html, "home-suite-coding-core")).toContain('data-lock="locked"');
		expect(html).toContain("4 of 4 admitted");
		expect(html).toContain("Lock stale");
		expect(html).toContain("Failed to load");
	});
});

describe("QuickActions and GetStartedCard", () => {
	it("offers the four actions locally and drops New experiment for a demo visitor", () => {
		const local = render(<QuickActions />);
		for (const key of ["newExperiment", "addSuite", "addAgent", "getStarted"]) expect(local).toContain(`home-action-${key}`);
		expect(tagOf(local, "home-action-addSuite")).toContain('href="/imports"');
		expect(render(<QuickActions />, demoSession())).not.toContain("home-action-newExperiment");
	});
	it("hides New experiment where the server has no launcher (QC2-27-03)", () => {
		api.health = { mode: "live", launch: false };
		try {
			expect(render(<QuickActions />)).not.toContain("home-action-newExperiment");
			expect(render(<GetStartedCard />)).not.toContain("home-get-started-new");
		} finally {
			api.health = undefined;
		}
	});
	it("links the get-started card to the quickstart", () => {
		const html = render(<GetStartedCard />);
		expect(tagOf(html, "home-get-started-open")).toContain('href="/quickstart"');
		expect(html).toContain("Set up the project");
	});
});