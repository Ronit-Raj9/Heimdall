import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { ImprovementEstimate } from "@/lib/types/improve";
import { describe, expect, it } from "vitest";
import { EstimateCard } from "./estimateCard";
import { ESTIMATE_FIXTURE } from "./improve.fixtures";
import {
	type ImprovementForm,
	NewImprovementForm,
	createBlock,
	improvementConfirmText,
	improvementFormProblems,
	improvementSpec,
	newForm,
	presetPatch,
} from "./newImprovementDialog";
import { PRESETS, PRESET_NAMES } from "./presetPicker";

// Round 4 lane F6 (PAID-19, PAID-23): the smallest size of Auto-improve.
const TRIAL_NOTE = "A trial this small can show the loop works. It cannot prove an improvement.";
const filled: ImprovementForm = { ...newForm(), name: "t-improve", agent: "a", suite: "hd-harbor-bfcl@1.0.0" };
const trialEstimate: ImprovementEstimate = {
	...ESTIMATE_FIXTURE,
	pool: 12,
	splits: { practice: 5, gate: 4, held_out: 3, impossible: 0 },
	runs: { ...ESTIMATE_FIXTURE.runs, total: 30 },
	usd: { ...ESTIMATE_FIXTURE.usd, low: 0.21, expected: 0.3, high: 0.6, limit: 3 },
	minutes: { low: 0, high: 0 },
	warnings: [{ code: "trial", message: TRIAL_NOTE }],
};
const props = {
	onChange: () => undefined,
	onSubmit: () => undefined,
	onCancel: () => undefined,
	agents: ["a"],
	suites: ["hd-harbor-bfcl@1.0.0"],
	model: "gpt-5-mini",
};

describe("Auto-improve small trial (round 4 F6)", () => {
	it("opens on the smallest size, first in the picker", () => {
		expect(newForm().preset).toBe("trial");
		expect(PRESET_NAMES[0]).toBe("trial");
		expect(PRESETS.trial).toMatchObject({ rounds: 1, ideas: 1, pool: 12, limit: 3 });
	});

	// The same body the server test cmd/bifrost-bench/jobs_improve_trial_f6_test.go (webTrialBody) parses and estimates.
	it("sends the trial's task count, rounds and ideas", () => {
		expect(improvementSpec(filled)).toEqual({
			kind: "improvement",
			name: "t-improve",
			agent: "agents/a.yaml",
			suite: "hd-harbor-bfcl@1.0.0",
			preset: "trial",
			editable: { instructions: true, files: [] },
			improver: { task: "heimdall-improver@1.1.0:reflect-edit", candidates: 1, keep: 1 },
			budget: { total_usd: 3 },
			rounds: 1,
			max_tasks: 12,
		});
		expect(improvementSpec({ ...filled, tasks: "20", rounds: "2", ideas: "2" })).toMatchObject({ max_tasks: 20, rounds: 2 });
	});

	it("refuses trial sizes the server refuses, in plain words", () => {
		expect(improvementFormProblems({ ...filled, tasks: "5" })).toContain("tasks");
		expect(improvementFormProblems({ ...filled, tasks: "41" })).toContain("tasks");
		expect(improvementFormProblems({ ...filled, rounds: "4" })).toContain("rounds");
		expect(improvementFormProblems({ ...filled, ideas: "3" })).toContain("ideas");
		expect(createBlock({ ...filled, tasks: "5" }, trialEstimate, false)).toBe("A trial uses 10 to 40 tasks.");
		// Custom: empty means every task; a number caps the pool.
		expect(improvementSpec({ ...filled, preset: "custom", tasks: "" }).max_tasks).toBeUndefined();
		expect(improvementSpec({ ...filled, preset: "custom", tasks: "30" }).max_tasks).toBe(30);
	});

	it("says before the start that a trial cannot prove a gain, with the task controls", () => {
		const html = render(<NewImprovementForm {...props} form={filled} agentFiles={[]} estimate={trialEstimate} />);
		expect(html).toContain('data-testid="new-improvement-trial-note"');
		expect(html).toContain(TRIAL_NOTE);
		expect(html).toContain('data-testid="new-improvement-tasks"');
		expect(html).toContain("Smallest (try it)");
		expect(html).toContain("Start run, up to $3.00");
	});

	it("confirms with the estimate, the runs and the cap", () => {
		const text = improvementConfirmText(filled, trialEstimate, "Spends keys.");
		expect(text).toBe(`Estimated $0.21 to $0.60, about 30 runs. It stops at $3.00. ${TRIAL_NOTE} Spends keys.`);
		expect(improvementConfirmText({ ...filled, preset: "small", limit: "15" }, undefined, "x")).toBe(
			"No estimate yet. It stops at $15.00. x",
		);
	});

	it("never shows about 0 to 0 minutes (PAID-19)", () => {
		const html = render(<EstimateCard estimate={trialEstimate} model="m" />);
		expect(html).not.toContain("0 to 0 minutes");
		expect(html).toContain("time not known yet");
	});

	it("keeps the typed limit when Custom is picked (PAID-23)", () => {
		expect(presetPatch({ ...filled, limit: "7" }, "custom")).toEqual({ preset: "custom", tasks: "" });
		expect(presetPatch({ ...filled, preset: "custom", limit: "7" }, "small")).toMatchObject({ limit: "15", tasks: "" });
		expect(presetPatch({ ...filled, preset: "small" }, "trial")).toMatchObject({ limit: "3", tasks: "12", rounds: "1" });
	});
});