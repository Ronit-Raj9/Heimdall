import type { AgentDiffConfigGroup, AgentDiffTask, AgentDiffVersionInfo } from "@/lib/types/bench";
import { agentDiffRequest } from "@/lib/store/apis/agentDiffApi";
import { describe, expect, it } from "vitest";
import {
	commonSuites,
	diffParams,
	fileDiffAsRunDiff,
	findVersion,
	formatConfigValue,
	formatSigned,
	groupVersionsByAgent,
	isChangedClass,
	runLinks,
	sortGroups,
	suggestBaseline,
	versionLabel,
	countSystemVersions,
	visibleTasks,
	type AgentDiffSearch,
} from "./agentDiff.utils";

const HASH_NEW = "sha256:67cd7a069ba6e6141a645638240d97c5062022fa81126e36300d8bc0006730cd";
const HASH_OLD = "sha256:895fa8468a3bcbdb1351f9dcdc550dc492c5df1daf7bbd092e4543f5c1c83758";
const HASH_OLDEST = "sha256:895fa846000000000000000000000000000000000000000000000000000000aa";
const HASH_OTHER = "sha256:e885efa97a924a21c4c94a63979a83b2267e1daedc256eca02c5986999d1ac10";

function version(config_hash: string, agent_id: string, latest_at: string, label = ""): AgentDiffVersionInfo {
	return { config_hash, agent_id, label, suites: ["coding-core@1.0.0"], arms: [], sealed_runs: 3, latest_at };
}

const versions = [
	version(HASH_OLD, "codex", "2026-11-13T09:00:00Z", "A: baseline"),
	version(HASH_OTHER, "claude-code", "2026-11-14T09:00:00Z"),
	version(HASH_NEW, "codex", "2026-11-14T10:00:00Z", "B: code wins"),
	version(HASH_OLDEST, "codex", "2026-11-01T09:00:00Z"),
];

function task(name: string, cls: AgentDiffTask["class"], pair?: AgentDiffTask["pair"]): AgentDiffTask {
	return { task: name, class: cls, pass_a: 0, n_a: 1, pass_b: 1, n_b: 1, rate_a: 0, rate_b: 1, pair };
}

const ref = (pair_id: string) => ({ run_id: `r_${pair_id}`, exp_id: "e_x", pair_id, outcome: "pass" });

describe("task classes", () => {
	it("treats flips and moves as changed, nothing else", () => {
		expect(["flipped_to_pass", "flipped_to_fail", "moved_to_pass", "moved_to_fail"].every(isChangedClass)).toBe(true);
		expect(["unchanged_pass", "unchanged_fail", "unstable_in_both", "other"].some(isChangedClass)).toBe(false);
	});

	it("shows flips and moves by default and every task with showAll", () => {
		const tasks = [
			task("t1", "flipped_to_pass"),
			task("t2", "unchanged_pass"),
			task("t3", "moved_to_fail"),
			task("t4", "unstable_in_both"),
		];
		expect(visibleTasks(tasks, false).map((x) => x.task)).toEqual(["t1", "t3"]);
		expect(visibleTasks(tasks, true).map((x) => x.task)).toEqual(["t1", "t2", "t3", "t4"]);
	});
});

describe("runLinks", () => {
	it("opens one pair when both runs share it", () => {
		expect(runLinks(task("t", "flipped_to_pass", { a: ref("p_a"), b: ref("p_b"), pair_id: "p_both" }))).toEqual({
			kind: "pair",
			pairId: "p_both",
		});
	});

	it("links each run through its own pair otherwise", () => {
		expect(runLinks(task("t", "moved_to_fail", { a: ref("p_a"), b: ref("p_b") }))).toEqual({ kind: "split", a: "p_a", b: "p_b" });
		expect(runLinks(task("t", "moved_to_fail", { a: ref(""), b: ref("p_b") }))).toEqual({ kind: "split", a: null, b: "p_b" });
	});

	it("links nothing without a pair or pair ids", () => {
		expect(runLinks(task("t", "unchanged_pass"))).toBeNull();
		expect(runLinks(task("t", "flipped_to_fail", { a: ref(""), b: ref("") }))).toBeNull();
	});
});

describe("sortGroups", () => {
	it("puts groups in report order and unknown ids last", () => {
		const group = (id: string) => ({ id, fields: [], files: [] }) as unknown as AgentDiffConfigGroup;
		const sorted = sortGroups([group("other"), group("future"), group("tools"), group("model")]);
		expect(sorted.map((g) => g.id)).toEqual(["model", "tools", "other", "future"]);
	});
});

describe("version lookup", () => {
	it("finds a version by full hash, bare hex or a unique 8+ prefix", () => {
		expect(findVersion(versions, HASH_NEW)?.config_hash).toBe(HASH_NEW);
		expect(findVersion(versions, HASH_NEW.slice("sha256:".length).toUpperCase())?.config_hash).toBe(HASH_NEW);
		expect(findVersion(versions, "67cd7a06")?.config_hash).toBe(HASH_NEW);
	});

	it("finds nothing for a short, ambiguous or arm selector", () => {
		expect(findVersion(versions, "67cd")).toBeNull();
		expect(findVersion(versions, "895fa846")).toBeNull();
		expect(findVersion(versions, "e_x:B")).toBeNull();
		expect(findVersion(versions, null)).toBeNull();
	});

	it("suggests the next older version of the same agent as the baseline", () => {
		expect(suggestBaseline(versions, HASH_NEW)?.config_hash).toBe(HASH_OLD);
		expect(suggestBaseline(versions, HASH_OLD)?.config_hash).toBe(HASH_OLDEST);
		expect(suggestBaseline(versions, HASH_OLDEST)).toBeNull();
		expect(suggestBaseline(versions, HASH_OTHER)).toBeNull();
		expect(suggestBaseline(versions, "e_x:B")).toBeNull();
	});

	it("groups versions by agent, newest first", () => {
		const groups = groupVersionsByAgent(versions);
		expect(groups.map((g) => g.agent)).toEqual(["claude-code", "codex"]);
		expect(groups[1].versions.map((v) => v.config_hash)).toEqual([HASH_NEW, HASH_OLD, HASH_OLDEST]);
	});

	it("labels a version with its label, short hash and agent", () => {
		expect(versionLabel(versions[0])).toBe("A: baseline · 895fa846…3758 · codex");
		expect(versionLabel(versions[1])).toBe("claude-code · e885efa9…ac10");
	});

	it("offers the suites both versions ran", () => {
		const a = { ...versions[0], suites: ["s@2", "s@1"] };
		const b = { ...versions[2], suites: ["s@1"] };
		expect(commonSuites(a, b)).toEqual(["s@1"]);
		expect(commonSuites(a, null)).toEqual(["s@2", "s@1"]);
		expect(commonSuites(null, null)).toEqual([]);
	});
});

describe("diffParams", () => {
	const empty: AgentDiffSearch = { a: null, b: null, suite: null, condition: null, agent: null, synthetic: null };

	it("asks nothing before a selection", () => {
		expect(diffParams(empty, null)).toBeNull();
		expect(diffParams({ ...empty, a: HASH_OLD }, null)).toBeNull();
		expect(diffParams({ ...empty, b: HASH_NEW }, null)).toBeNull();
	});

	it("passes A, B, suite, condition and synthetic", () => {
		expect(diffParams({ a: HASH_OLD, b: HASH_NEW, suite: "s@1", condition: "probe-x", agent: "codex", synthetic: "1" }, null)).toEqual({
			a: HASH_OLD,
			b: HASH_NEW,
			suite: "s@1",
			condition: "probe-x",
			synthetic: "1",
		});
	});

	it("uses the suggested baseline when only B is set", () => {
		expect(diffParams({ ...empty, b: HASH_NEW }, HASH_OLD)).toEqual({ a: HASH_OLD, b: HASH_NEW });
	});

	it("asks for an agent's two newest versions when only the agent is set", () => {
		expect(diffParams({ ...empty, agent: "codex", synthetic: "0" }, null)).toEqual({ agent: "codex" });
	});
});

describe("agentDiffRequest", () => {
	it("leaves out empty params and uses the bare route without any", () => {
		expect(agentDiffRequest({})).toBe("/agents/diff");
		expect(agentDiffRequest({ a: "x", b: "", suite: undefined })).toEqual({ url: "/agents/diff", params: { a: "x" } });
	});
});

describe("formatting", () => {
	it("shows config values as inert text and null as unset", () => {
		expect(formatConfigValue("plain")).toBe("plain");
		expect(formatConfigValue(40)).toBe("40");
		expect(formatConfigValue({ k: ["v"] })).toBe('{"k":["v"]}');
		expect(formatConfigValue(null)).toBeNull();
	});

	it("signs deltas except zero", () => {
		expect(formatSigned(3, "en")).toBe("+3");
		expect(formatSigned(-0.009, "en")).toBe("-0.009");
		expect(formatSigned(0, "en")).toBe("0");
	});

	it("wraps a file diff for the run diff renderer", () => {
		expect(fileDiffAsRunDiff({ path: "CLAUDE.md", diff: "+x\n" })).toMatchObject({ patch: "+x\n", diff: { path: "CLAUDE.md", files: [] } });
		expect(fileDiffAsRunDiff({ path: "a" }).patch).toBe("");
	});
});

describe("version picker rows (N159)", () => {
	const saved = { ...version(HASH_OLD, "codex", "2026-11-13T09:00:00Z", "gpt-5.5"), version: 1, model: "gpt-5.5" };
	const unsaved = { ...version(HASH_NEW, "codex", "2026-11-14T10:00:00Z", "gpt-6-luna"), model: "gpt-6-luna" };
	const oracle = { ...version(HASH_OTHER, "oracle", "2026-11-15T09:00:00Z", "right answer, try 1"), system: true };

	it("leaves system checks out unless asked, and lists saved versions before unsaved setups", () => {
		const groups = groupVersionsByAgent([saved, unsaved, oracle]);
		expect(groups.map((g) => g.agent)).toEqual(["codex"]);
		expect(groups[0].versions.map((v) => v.config_hash)).toEqual([HASH_OLD, HASH_NEW]);
		expect(groupVersionsByAgent([saved, unsaved, oracle], { showSystem: true }).map((g) => g.agent)).toEqual(["codex", "oracle"]);
		expect(countSystemVersions([saved, unsaved, oracle])).toBe(1);
	});
	it("names the saved version and model, or says the setup was not saved", () => {
		expect(versionLabel(saved, "Unsaved setup")).toBe("v1 · gpt-5.5 · 895fa846…3758");
		expect(versionLabel(unsaved, "Unsaved setup")).toBe("Unsaved setup · gpt-6-luna · 67cd7a06…30cd");
	});
});