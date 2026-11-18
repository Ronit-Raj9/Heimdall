import type { AgentSummary } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import { agentKey, pickAgent } from "../../app/agents/views/agentConfigsTab";
import { triageExpId } from "../../app/inbox/views/triageTab";
import { suiteTiers } from "../suites/suiteTiers";
import { buildKind, frameworkRows } from "./frameworks.utils";

const agent = (over: Partial<AgentSummary>): AgentSummary =>
	({
		id: "a",
		path: "agents/a.yaml",
		adapter: "command",
		image: "",
		dockerfile: "",
		base_arg: "",
		per_suite_build: false,
		parser: "",
		evidence_level: "gateway",
		model: "",
		scripted_kind: "",
		notes_project_file: "",
		notes_user_file: "",
		budgets: { wall_s: 1, cpus: 1, mem_gb: 1 },
		gateway: { provider: "openai" },
		env_names: [],
		file_paths: [],
		sealed_runs: null,
		...over,
	}) as AgentSummary;

describe("frameworks matrix", () => {
	it("words the build the way the agent card does", () => {
		expect(buildKind({ per_suite_build: true, dockerfile: "x" })).toBe("perSuite");
		expect(buildKind({ per_suite_build: false, dockerfile: "Dockerfile" })).toBe("oneImage");
		expect(buildKind({ per_suite_build: false, dockerfile: "" })).toBe("pulled");
	});

	it("lists real agents before scripted controls, by id, and leaves out configs that failed to load", () => {
		const rows = frameworkRows([
			agent({ id: "oracle", scripted_kind: "oracle" }),
			agent({ id: "zeta", parser: "codex", reports_events: "native" }),
			agent({ id: "", path: "agents/broken.yaml", error: "bad yaml" }),
			agent({ id: "alpha", gateway: { provider: "anthropic", fallbacks: ["openai"] } as AgentSummary["gateway"] }),
		]);
		expect(rows.map((row) => row.id)).toEqual(["alpha", "zeta", "oracle"]);
		expect(rows[0]).toMatchObject({ provider: "anthropic", fallbacks: 1, nativeTrace: false });
		expect(rows[1]).toMatchObject({ nativeTrace: true, parser: "codex", selfReported: true });
		expect(rows[2].scripted).toBe("oracle");
	});
});

describe("agents configs tab", () => {
	const list = [agent({ id: "", path: "agents/broken.yaml", error: "x" }), agent({ id: "codex" }), agent({ id: "toy" })];

	it("opens the config ?agent= names, else the first that loaded", () => {
		expect(pickAgent(list, "toy")?.id).toBe("toy");
		expect(pickAgent(list, null)?.id).toBe("codex");
		expect(pickAgent(list, "gone")?.id).toBe("codex");
		expect(pickAgent([], null)).toBeNull();
	});

	it("keys a config that failed to load by its file", () => {
		expect(agentKey(list[0])).toBe("agents/broken.yaml");
		expect(agentKey(list[1])).toBe("codex");
	});
});

describe("suite tiers", () => {
	it("falls back to nightly, every task, when the server sends no tiers", () => {
		const tasks = [{ id: "t01" }, { id: "t02" }] as never;
		expect(suiteTiers({ tasks })).toEqual([{ name: "nightly", tasks: ["t01", "t02"], defined: false }]);
		const tiers = [{ name: "pr", tasks: ["t01"], defined: true }];
		expect(suiteTiers({ tasks, tiers })).toBe(tiers);
	});
});

describe("triage experiment", () => {
	it("reads the first id of a comma list carried over from the Clusters tab", () => {
		expect(triageExpId("e_1")).toBe("e_1");
		expect(triageExpId("e_1, e_2")).toBe("e_1");
		expect(triageExpId("")).toBeNull();
		expect(triageExpId(null)).toBeNull();
	});
});