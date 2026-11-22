import "@/lib/i18n";

import type { AgentCompatibility, EvaluationPack } from "@/lib/store/apis/evaluationsApi";
import { mutation } from "@/lib/testing/render";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { Step3BenchmarksView } from "./step3Benchmarks";

vi.mock("@/lib/store/apis/suiteAuthoringApi", async (orig) => ({
	...(await orig<object>()),
	useCreateSuiteMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/suiteBuildApi", () => ({
	useImportSuiteRowsMutation: () => mutation(),
	useGetRowImportQuery: () => ({ data: undefined }),
	useSetSuiteGraderMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/jobRunApi", () => ({ useCreateJobMutation: () => mutation() }));
vi.mock("@/lib/store/hooks", () => ({ useAppDispatch: () => vi.fn() }));

const s = (id: string, admitted_count: number) => ({ id, description: "", admitted_count, environment: "docker" }) as never;

const suites = [s("longmemeval", 26), s("locomo", 26), s("hd-harbor-tb2", 80), s("hd-swe-verified", 500), s("tau2", 40)];

const packs: EvaluationPack[] = [
	{ id: "memory", name: "Memory", benchmarks: ["longmemeval", "locomo"], capability: "memory" },
	{ id: "coding", name: "Coding agent", benchmarks: ["hd-harbor-tb2", "hd-swe-verified"], capability: "coding" },
	{ id: "mixed", name: "Mixed", benchmarks: ["hd-harbor-tb2", "longmemeval", "tau2"], capability: "coding" },
];

const compat: AgentCompatibility = {
	agent: "zz-test-loop",
	capabilities: ["shell"],
	benchmarks: [
		{
			id: "longmemeval",
			ok: false,
			requires: ["sessions"],
			missing: ["sessions"],
			reason: "agent zz-test-loop cannot run benchmark longmemeval: needs sessions",
		},
		{ id: "locomo", ok: false, requires: ["sessions"], missing: ["sessions"], reason: "" },
		{ id: "hd-harbor-tb2", ok: true, requires: ["shell"], missing: [], reason: "" },
	],
};

const card = (id: string) => {
	const html = renderToStaticMarkup(
		<Step3BenchmarksView suites={suites} packs={packs} compatibility={compat} selected={[]} onChange={() => {}} />,
	);
	return (html.match(new RegExp(`data-testid="step3-pack-${id}"[\\s\\S]*?</button>`))?.[0] ?? "").replace(/<!-- -->/g, "");
};

describe("pack card counts (N99)", () => {
	it("pack with no runnable member names the reason and no task count", () => {
		const memory = card("memory");
		expect(memory).toContain("2 benchmarks, none your agent can run: needs sessions");
		expect(memory).not.toContain("ready task");
		expect(memory).toContain('disabled=""');
	});
	it("says how many members the agent can run and whose tasks it counts", () => {
		expect(card("mixed")).toContain("3 benchmarks, 2 your agent can run · 120 ready tasks it can run");
	});
	it("keeps the short text when every member runs", () => {
		expect(card("coding")).toContain("2 benchmarks · 580 ready tasks");
	});
});