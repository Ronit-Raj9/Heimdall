import "@/lib/i18n";

import type { AgentCompatibility, EvaluationPack } from "@/lib/store/apis/evaluationsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { mutation, render as renderIn } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { benchmarkSlug, isTaken, OwnTasksCard } from "./ownTasks";
import {
	Step3BenchmarksView,
	benchmarkCapability,
	compatFor,
	compatibilityMap,
	groupBenchmarks,
	incompatibleReason,
	keepRunnable,
	packSelection,
} from "./step3Benchmarks";

const own = vi.hoisted(() => ({ create: {} as Record<string, unknown> }));
vi.mock("@/lib/store/apis/suiteAuthoringApi", async (orig) => ({
	...(await orig<object>()),
	useCreateSuiteMutation: () => mutation(own.create),
}));
vi.mock("@/lib/store/apis/suiteBuildApi", () => ({
	useImportSuiteRowsMutation: () => mutation(),
	useGetRowImportQuery: () => ({ data: undefined }),
	useSetSuiteGraderMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/hooks", () => ({ useAppDispatch: () => vi.fn() }));

const s = (id: string, admitted_count: number, extra: Record<string, unknown> = {}) =>
	({ id, description: "", admitted_count, environment: "docker", ...extra }) as never;

const suites = [
	s("hd-harbor-tb2", 80, { builtin: true }),
	s("hd-swe-verified", 500, { builtin: true }),
	s("longmemeval", 0, { builtin: true }),
	s("kernelbench-l2", 100, { builtin: true }),
	s("voicebench", 40, { builtin: true }),
	s("acme-orders", 6),
	s("coding-core", 3),
	s("my-support", 7, { track: "support" }),
];

const packs: EvaluationPack[] = [
	{ id: "coding", name: "Coding agent", benchmarks: ["hd-harbor-tb2", "hd-swe-verified", "hd-swe-rebench"], capability: "coding" },
	{ id: "voice", name: "Voice agent", benchmarks: ["voicebench"], capability: "voice" },
];

const compat: AgentCompatibility = {
	agent: "codex",
	capabilities: ["shell", "tools", "mcp"],
	benchmarks: [
		{ id: "hd-harbor-tb2", ok: true, requires: ["shell"], missing: [], reason: "" },
		{
			id: "voicebench",
			ok: false,
			requires: ["voice"],
			missing: ["voice"],
			reason: "agent codex cannot run benchmark voicebench: needs voice",
		},
		{ id: "kernelbench-l2", ok: false, requires: ["gpu"], missing: ["gpu"], reason: "" },
	],
};

const render = (selected: string[] = [], pack?: string) =>
	renderToStaticMarkup(
		<Step3BenchmarksView suites={suites} packs={packs} compatibility={compat} selected={selected} pack={pack} onChange={() => {}} />,
	);
const block = (html: string, testId: string) => html.match(new RegExp(`data-testid="${testId}"[\\s\\S]*?</label>`))?.[0] ?? "";

describe("benchmark grouping", () => {
	it("maps benchmarks to capability groups", () => {
		expect(benchmarkCapability({ id: "hd-harbor-tb2" })).toBe("terminal");
		expect(benchmarkCapability({ id: "hd-swe-verified" })).toBe("coding");
		expect(benchmarkCapability({ id: "hd-swe-ib-lcb" })).toBe("safety");
		expect(benchmarkCapability({ id: "kernelbench-l2" })).toBe("gpu");
		expect(benchmarkCapability({ id: "browsecomp-plus-0" })).toBe("research");
		expect(benchmarkCapability({ id: "my-support", track: "support" })).toBe("support");
	});

	it("leaves out practice examples and benchmarks with no ready task, and orders groups", () => {
		const { groups } = groupBenchmarks(suites);
		expect(groups.map((g) => g.capability)).toEqual(["terminal", "coding", "support", "voice", "gpu"]);
		const ids = groups.flatMap((g) => g.suites.map((x: { id: string }) => x.id));
		expect(ids).not.toContain("longmemeval");
		expect(ids).not.toContain("acme-orders");
		expect(ids).not.toContain("coding-core");
	});

	it("explains why a benchmark cannot run", () => {
		expect(incompatibleReason(undefined)).toBe("");
		expect(incompatibleReason(compat.benchmarks[1])).toContain("needs voice");
		expect(incompatibleReason(compat.benchmarks[2])).toBe("Needs gpu");
	});

	it("picks only present, runnable pack members", () => {
		const map = new Map(compat.benchmarks.map((b) => [b.id, b]));
		expect(packSelection(packs[0], suites, map)).toEqual(["hd-harbor-tb2", "hd-swe-verified"]);
		expect(packSelection(packs[1], suites, map)).toEqual([]);
		const memory: EvaluationPack = { id: "memory", name: "Memory", benchmarks: ["longmemeval"], capability: "memory" };
		expect(packSelection(memory, suites, map)).toEqual([]);
	});
});

describe("Step3BenchmarksView", () => {
	it("shows pack cards with benchmark and ready task counts", () => {
		const html = render();
		const coding = html.match(/data-testid="step3-pack-coding"[\s\S]*?<\/button>/)?.[0] ?? "";
		expect(coding).toContain("Coding agent");
		expect(coding).toContain("2 benchmarks");
		expect(coding).toContain("580 ready tasks");
		expect(html).toMatch(/data-testid="step3-pack-voice"[^>]*disabled=""|disabled=""[^>]*data-testid="step3-pack-voice"/);
	});

	it("greys out incompatible benchmarks with the reason and marks KernelBench as GPU", () => {
		const html = render();
		const voice = block(html, "step3-benchmark-voicebench");
		expect(voice).toContain('data-disabled="true"');
		expect(voice).toContain("needs voice");
		expect(block(html, "step3-benchmark-kernelbench-l2")).toContain("Needs gpu");
		const ok = block(html, "step3-benchmark-hd-harbor-tb2");
		expect(ok).not.toContain("data-disabled");
		expect(ok).toContain("Terminal-Bench 2");
		expect(ok).toContain("80 ready tasks");
		expect(html).not.toContain('data-testid="step3-benchmark-longmemeval"');
	});

	it("shows the GPU worker note when compatibility is unknown", () => {
		const html = renderToStaticMarkup(<Step3BenchmarksView suites={suites} packs={[]} selected={[]} onChange={() => {}} />);
		expect(block(html, "step3-benchmark-kernelbench-l2")).toContain("Needs a GPU worker");
	});

	it("offers no practice examples", () => {
		const html = render();
		expect(html).not.toContain("step3-practice");
		expect(html).not.toContain("Practice examples");
		expect(html).not.toContain('data-testid="step3-benchmark-acme-orders"');
	});

	it("summarises the selection", () => {
		expect(render()).toContain("No benchmarks selected");
		const html = render(["hd-harbor-tb2", "hd-swe-verified"], "coding");
		expect(html).toMatch(/aria-pressed="true"[^>]*data-testid="step3-pack-coding"/);
		const summary = html.match(/data-testid="step3-summary"[\s\S]*?<\/div>/)?.[0] ?? "";
		expect(summary).toContain("2 benchmarks");
		expect(summary).toContain("580 ready tasks");
	});
});
describe("compatibility matching", () => {
	it("matches rows by id without a version pin and tolerates other row shapes", () => {
		const raw = {
			agent: "codex",
			capabilities: ["shell"],
			benchmarks: [
				{ id: "voicebench@1.0.0", ok: false, requires: ["voice"], missing: ["voice"], reason: "" },
				{ benchmark: "kernelbench-l2", missing: ["gpu"], requires: null, reason: "" },
			],
		} as unknown as AgentCompatibility;
		const map = compatibilityMap(raw);
		expect(incompatibleReason(compatFor(map, "voicebench"))).toBe("Needs voice");
		expect(incompatibleReason(compatFor(map, "kernelbench-l2"))).toBe("Needs gpu");
		const html = renderToStaticMarkup(<Step3BenchmarksView suites={suites} packs={packs} compatibility={raw} selected={[]} onChange={() => {}} />);
		expect(block(html, "step3-benchmark-voicebench")).toContain('data-disabled="true"');
		expect(block(html, "step3-benchmark-voicebench")).toMatch(/<input[^>]*disabled=""/);
	});

	it("greys out tool benchmarks a remote agent cannot run and notes skipped tasks", () => {
		const raw = {
			agent: "bot",
			capabilities: ["tools"],
			benchmarks: [
				{ id: "voicebench", ok: false, requires: [], missing: [], reason: "Agent bot cannot run benchmark voicebench: turn on tool access (remote.tools: expose)." },
				{ id: "hd-harbor-tb2", ok: true, requires: [], missing: [], reason: "", note: "2 of 10 tasks are skipped: needs tools." },
			],
		} as unknown as AgentCompatibility;
		const html = renderToStaticMarkup(<Step3BenchmarksView suites={suites} packs={packs} compatibility={raw} selected={[]} onChange={() => {}} />);
		expect(block(html, "step3-benchmark-voicebench")).toContain('data-disabled="true"');
		expect(block(html, "step3-benchmark-voicebench")).toContain("turn on tool access");
		expect(block(html, "step3-benchmark-hd-harbor-tb2")).not.toContain('data-disabled="true"');
		expect(block(html, "step3-benchmark-hd-harbor-tb2")).toContain("2 of 10 tasks are skipped");
	});

	it("waits for the answer before rows can be picked", () => {
		const html = renderToStaticMarkup(<Step3BenchmarksView suites={suites} packs={packs} selected={[]} onChange={() => {}} checking />);
		expect(block(html, "step3-benchmark-voicebench")).toContain('data-disabled="true"');
		expect(block(html, "step3-benchmark-hd-harbor-tb2")).toContain("Checking whether your agent can run it");
	});

	it("drops picked benchmarks the agent cannot run", () => {
		expect(keepRunnable(["hd-harbor-tb2", "voicebench", "acme-orders"], compat)).toEqual(["hd-harbor-tb2", "acme-orders"]);
		expect(keepRunnable(["voicebench"], undefined)).toEqual(["voicebench"]);
	});
});

describe("your own tasks card", () => {
	it("comes first in step 3", () => {
		const html = renderToStaticMarkup(
			<Step3BenchmarksView suites={suites} packs={packs} selected={[]} onChange={() => {}} ownTasks={<OwnTasksCard onAdd={() => {}} />} />,
		);
		expect(html.indexOf('data-testid="step3-own"')).toBeGreaterThan(-1);
		expect(html.indexOf('data-testid="step3-own"')).toBeLessThan(html.indexOf("Start from a pack"));
		expect(html).toContain("Your own tasks");
		expect(html).toContain('data-testid="step3-own-open"');
	});
	it("asks for a name and grader before creating the benchmark", () => {
		own.create = {};
		const html = renderIn(<OwnTasksCard onAdd={() => {}} initialOpen />);
		expect(html).toContain('data-testid="step3-own-name"');
		expect(html).toContain('data-testid="step3-own-grader-exact"');
		expect(html).toContain('data-testid="step3-own-grader-judge"');
		expect(html).toMatch(/disabled=""[^>]*data-testid="step3-own-create"|data-testid="step3-own-create"[^>]*disabled=""/);
		expect(html.replace(/<[^>]+>/g, " ")).not.toMatch(/suite|probe|canary|sealed|lease|runner|\u2014|\u2013/i);
	});
	it("says when the name is taken and suggests another", () => {
		own.create = { error: { status: 409, data: { error: { message: "suite exists" } } } };
		const html = renderIn(<OwnTasksCard onAdd={() => {}} initialOpen />);
		expect(html).toContain('data-testid="step3-own-error"');
		expect(html).toContain("is taken. Try another name");
		own.create = {};
	});
	it("imports rows with the mapping step and explains readiness", () => {
		const html = renderIn(<OwnTasksCard onAdd={() => {}} initialSuite={{ id: "support-faq", grader: "judge" }} />);
		expect(html).toContain("Benchmark support-faq created.");
		expect(html).toContain('data-testid="suite-build-rows-data"');
		expect(html).toContain('data-testid="suite-build-verbatim"');
		expect(html).toContain('data-testid="step3-own-format-tickets"');
		expect(html).not.toContain('data-testid="suite-build-rows-match"');
		expect(html).toContain("Tasks that fail are listed here and not counted.");
		expect(html).toContain("Launch when at least one task is ready.");
		expect(html).not.toContain('data-testid="job-run-suite.admit"');
		expect(html.replace(/<[^>]+>/g, " ")).not.toMatch(/probe|canary|sealed|lease|runner|\u2014|\u2013/i);
	});
	it("makes a benchmark id from the name", () => {
		expect(benchmarkSlug("  Support FAQ (v2)! ")).toBe("support-faq-v2");
		expect(benchmarkSlug("---")).toBe("");
		expect(benchmarkSlug("a".repeat(70))).toHaveLength(64);
		expect(isTaken({ status: 409 })).toBe(true);
		expect(isTaken({ status: 400, data: { error: { message: "suite already exists" } } })).toBe(true);
		expect(isTaken({ status: 400, data: { error: { message: "bad id" } } })).toBe(false);
	});
});
