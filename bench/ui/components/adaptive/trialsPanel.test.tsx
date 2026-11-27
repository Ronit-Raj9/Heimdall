import "@/lib/i18n";

import { experimentAdaptivePath } from "@/lib/store/apis/adaptiveApi";
import type { AdaptiveView } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { formatUSD, hasContent, TrialsPanel } from "./trialsPanel";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

// Shaped after the $0 adaptive smoke (docs/adaptive-trials.md, "End to end"): a task-rule
// experiment on the pr tier where concordant tasks stopped at the minimum.
const taskView: AdaptiveView = {
	exp_id: "e_hd_w7_task",
	tier: "pr",
	tier_tasks: ["t01", "t02", "t03", "t04"],
	trials: { mode: "adaptive", rule: "task", min: 1, max: 3 },
	plan: { rule: "task", conditions: ["clean"], min: 1, max: 3, metric: "verified_success", alpha: 0.05, optional: 16, required: 8 },
	report: {
		rule: "task",
		max_slots: 24,
		required_slots: 16,
		run_slots: 16,
		runs_saved: 8,
		stop_reason: "max_reached",
		decision_alpha: 0.05,
		tasks: [
			{ condition: "clean", task: "t01", trials: 3, max: 3, reason: "max_reached" },
			{ condition: "clean", task: "t03", trials: 1, max: 3, reason: "concordant" },
		],
		conditions: ["clean"],
	},
	more_evidence: null,
	extends: null,
	decision: "INCONCLUSIVE",
	max_slots: 24,
};

const seqView: AdaptiveView = {
	...taskView,
	exp_id: "e_hd_w7_seq",
	tier: "",
	tier_tasks: [],
	trials: { mode: "adaptive", rule: "sequential", looks: 2 },
	plan: {
		rule: "sequential",
		conditions: ["clean"],
		metric: "verified_success",
		looks: [["t01"], ["t02"]],
		look_alphas: [0.0056, 0.0444],
		alpha: 0.05,
		futility: "curtail",
		optional: 8,
		required: 8,
	},
	report: {
		rule: "sequential",
		max_slots: 16,
		required_slots: 8,
		run_slots: 8,
		runs_saved: 8,
		stop_reason: "futility",
		stop_look: 1,
		looks: 2,
		decision_alpha: 0.0056,
		look_results: [
			{ look: 1, tasks: 4, alpha: 0.0056, p_low: 1, p_high: 1, d_bar: 0, outcome: "futility", best_case_p: 0.0625 },
			{ look: 2, tasks: 0, alpha: 0.0444, d_bar: 0, outcome: "not_reached" },
		],
	},
	more_evidence: {
		target_pp: 10,
		tasks: 52,
		tasks_total: 60,
		sd: 0.3,
		sd_assumed: true,
		runs: 208,
		cost_usd: 0,
		cost_basis: "mean cost 0.0000 USD per run over 8 sealed runs of this experiment, times 4 runs per task",
		command: "heimdall extend -exp e_hd_w7_seq -tasks 52",
		combination: "exploratory",
	},
	extends: { exp_id: "e_first", manifest_hash: "h", kind: "tasks", rule: "exploratory" },
};

describe("TrialsPanel", () => {
	it("says more than the cap, not zero tasks, when the sizing is over (audit 1-B-06)", () => {
		const over = {
			...seqView,
			more_evidence: {
				...seqView.more_evidence!,
				over: true,
				tasks: 0,
				tasks_total: 200,
				runs: 0,
				cost_usd: undefined,
				command: undefined,
			},
		};
		const html = renderToStaticMarkup(<TrialsPanel view={over} />);
		expect(html).toContain("more than 200 tasks in all");
		expect(html).not.toContain("about 0 more tasks");
	});

	it("shows the tier, trials used, runs saved and the stopping reason of a task rule", () => {
		const html = renderToStaticMarkup(<TrialsPanel view={taskView} />);
		expect(html).toContain('data-testid="trials-panel"');
		expect(html).toContain("tier pr: 4 tasks");
		expect(html).toContain("16 of 24 slots");
		expect(html).toContain('data-testid="trials-saved"');
		expect(html).toContain("some tasks used every trial");
		expect(html).toContain("1 of 2 tasks stopped early");
		expect(html).toContain("common tasks");
	});

	it("shows looks, the stop look and what more evidence costs", () => {
		const html = renderToStaticMarkup(<TrialsPanel view={seqView} />);
		expect(html).toContain('data-testid="trials-looks"');
		expect(html).toContain("no later look could decide at look 1 of 2");
		expect(html).toContain("To detect 10.0 pp you need about 52 more tasks, estimated cost $0.0000.");
		expect(html).toContain("SD 0.30 assumed");
		expect(html).toContain("pooling it with this comparison is exploratory");
		expect(html).toContain("heimdall extend -exp e_hd_w7_seq -tasks 52");
		expect(html).toContain("follow-up of e_first");
	});

	it("has nothing to show for a classic fixed experiment", () => {
		expect(hasContent({ ...taskView, tier: "", plan: null, report: null, trials: null })).toBe(false);
		expect(hasContent(taskView)).toBe(true);
		expect(formatUSD(12.5)).toBe("$12.50");
	});

	it("escapes the experiment id in the request path", () => {
		expect(experimentAdaptivePath("e/x")).toBe("/experiments/e%2Fx/adaptive");
	});
});

// The Go side's key lists (bench/handlers/testdata/contract/<Type>.keys): the fixtures above carry
// only listed keys and every required one.
const CONTRACT_DIR = path.resolve(__dirname, "../../../handlers/testdata/contract");

function keyList(typ: string): { required: string[]; optional: string[] } {
	const lines = fs
		.readFileSync(path.join(CONTRACT_DIR, `${typ}.keys`), "utf8")
		.split("\n")
		.map((l) => l.trim())
		.filter((l) => l && !l.startsWith("#"));
	return { required: lines.filter((l) => !l.endsWith("?")), optional: lines.filter((l) => l.endsWith("?")).map((l) => l.slice(0, -1)) };
}

function checkObject(obj: object, typ: string) {
	const { required, optional } = keyList(typ);
	const keys = Object.keys(obj);
	for (const k of required) expect(keys, `${typ} needs ${k}`).toContain(k);
	for (const k of keys) expect([...required, ...optional], `${typ} has no key ${k}`).toContain(k);
}

describe("adaptive contract", () => {
	it("matches the Go key lists", () => {
		for (const v of [taskView, seqView]) {
			checkObject(v, "AdaptiveView");
			if (v.trials) checkObject(v.trials, "TrialsPolicy");
			if (v.plan) checkObject(v.plan, "AdaptivePlan");
			if (v.report) {
				checkObject(v.report, "AdaptiveReport");
				v.report.tasks?.forEach((x) => checkObject(x, "AdaptiveTaskState"));
				v.report.look_results?.forEach((x) => checkObject(x, "AdaptiveLook"));
			}
			if (v.more_evidence) checkObject(v.more_evidence, "MoreEvidence");
			if (v.extends) checkObject(v.extends, "ExtendsRef");
		}
	});
});