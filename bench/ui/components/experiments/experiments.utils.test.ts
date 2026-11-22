import type { ExperimentStatus } from "@/lib/types/bench";
import { describe, expect, it } from "vitest";
import {
	experimentPickerOptions,
	exportPickerGroups,
	humanExperimentName,
	isAutoName,
	isDebugExperiment,
	isHiddenByDefault,
	newestExperiment,
	readWriteToken,
	saveWriteToken,
	type StatusWithDebug,
} from "./experiments.utils";

function status(exp_id: string, frozen_at: string, started_at?: string): ExperimentStatus {
	return {
		exp_id,
		name: exp_id,
		state: "done",
		manifest_hash: "h",
		frozen_at,
		started_at,
		slots: { total: 1, done: 1, running: 0, failed: 0 },
		spent_usd: { state: "observed", v: 0 },
	};
}

describe("newestExperiment", () => {
	it("picks the latest start, else the latest freeze", () => {
		const list = [
			status("e_old", "2026-11-10T00:00:00Z", "2026-11-10T01:00:00Z"),
			status("e_new", "2026-11-12T00:00:00Z"),
			status("e_mid", "2026-11-11T00:00:00Z", "2026-11-11T05:00:00Z"),
		];
		expect(newestExperiment(list)?.exp_id).toBe("e_new");
	});
	it("is null for an empty list", () => {
		expect(newestExperiment([])).toBeNull();
	});
});

describe("write token storage", () => {
	it("is a no-op without a browser", () => {
		saveWriteToken("secret");
		expect(readWriteToken()).toBe("");
	});
});
describe("experiment names and pickers (fix wave 2)", () => {
	const tx = (k: string, o?: Record<string, string>) => `${k}|${Object.values(o ?? {}).join("|")}`;
	const evalNames = new Map([["ev_1", "codex: two models"]]);
	const child = {
		...status("e_eval_child", "2026-11-22T14:00:00Z"),
		name: "eval-20261122-143334-423797-hd-harbor-bfcl",
		suite: "hd-harbor-bfcl@1.0.0",
		arms: ["A", "B"],
		evaluation_id: "ev_1",
	};
	const replay = {
		...status("e_replay_r_n697kiewjt_x4qq", "2026-11-23T10:00:00Z"),
		name: "replay r_n697kiewjt",
		suite: "swe-lite@1.0.0",
		arms: ["A"],
	};
	const admit = { ...status("e_admit_acme_safety_5e8q", "2026-11-23T09:00:00Z"), name: "admit-acme-safety", suite: "acme-safety@1.0.0" };
	const real = { ...status("e_real", "2026-11-20T09:00:00Z"), name: "Prompt v2 vs v1", suite: "swe-lite@1.0.0", arms: ["A", "B"] };

	it("treats an evaluation child name as generated and names it after its evaluation", () => {
		expect(isAutoName(child)).toBe(true);
		expect(humanExperimentName(child, tx, evalNames)).toBe("evaluationChild|codex: two models|BFCL function calling");
		expect(humanExperimentName(child, tx)).toBe("quickRun|hd-harbor-bfcl");
	});
	it("knows debug runs by field and by old id", () => {
		expect(isDebugExperiment({ exp_id: "e_replay_r_x_ab12" })).toBe(true);
		expect(isDebugExperiment({ exp_id: "e_x", debug: "fork" })).toBe(true);
		expect(isDebugExperiment({ exp_id: "e_real" })).toBe(false);
		expect(isHiddenByDefault({ exp_id: "e_replay_r_x_ab12" })).toBe(true);
		expect(isHiddenByDefault({ exp_id: "e_x", debug: "fork" } as StatusWithDebug)).toBe(true);
		expect(humanExperimentName(replay, tx)).toBe("debug.replay|r_n697kiewjt");
		expect(humanExperimentName({ ...replay, exp_id: "e_x", debug: "fork", debug_from_run: "r_9" } as StatusWithDebug, tx)).toBe(
			"debug.fork|r_9",
		);
	});
	it("lists real comparisons first and hides checks and debug runs unless asked", () => {
		const list = [admit, replay, real, child];
		expect(experimentPickerOptions(list, tx, evalNames).map((o) => o.id)).toEqual(["e_eval_child", "e_real"]);
		expect(experimentPickerOptions(list, tx, evalNames, { showHidden: true }).map((o) => o.id)).toEqual([
			"e_replay_r_n697kiewjt_x4qq",
			"e_admit_acme_safety_5e8q",
			"e_eval_child",
			"e_real",
		]);
		const first = experimentPickerOptions(list, tx, evalNames)[0];
		expect(first.label).toBe("evaluationChild|codex: two models|BFCL function calling");
		expect(first.secondary).toBe("e_eval_child");
	});
	it("groups export choices into evaluations, comparisons and the rest", () => {
		const single = { ...child, exp_id: "e_eval_single", arms: ["A"] };
		const groups = exportPickerGroups([admit, replay, real, child, single], tx, evalNames, true);
		expect(exportPickerGroups([admit, replay, real], tx, evalNames).other).toEqual([]);
		expect(groups.evaluations.map((o) => o.id)).toEqual(["e_eval_child", "e_eval_single"]);
		expect(groups.comparisons.map((o) => o.id)).toEqual(["e_real"]);
		expect(groups.other.map((o) => o.id)).toEqual(["e_replay_r_n697kiewjt_x4qq", "e_admit_acme_safety_5e8q"]);
	});
});