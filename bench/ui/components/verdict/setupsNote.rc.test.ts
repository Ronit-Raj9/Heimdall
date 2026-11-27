import i18n from "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { plainSetupsNote, sharedTasks } from "./setupsTable";
import { costHeadline } from "./verdictSummary";

const t = i18n.getFixedT("en", "ufrc") as unknown as (k: string, o?: Record<string, unknown>) => string;
const pair = (tasks: number) => ({
	arm_x: "A",
	arm_y: "B",
	tasks,
	n_nonzero: 1,
	mean_diff: 0,
	p: 1,
	p_adjusted: 1,
	significant: false,
	reason: "",
});

// Round 2 lane RC: plain words for the server's setups note (Q-140) and the tile's cost headline (Q-49).
describe("plainSetupsNote", () => {
	it("says too few shared tasks in plain words, no statistics terms", () => {
		const text = plainSetupsNote({ leader: "A", separable: false, pairs: [pair(3), pair(5)] }, t);
		expect(sharedTasks({ pairs: [pair(3), pair(5)] })).toBe(5);
		expect(text).toBe("Only 5 shared tasks, too few to tell these setups apart. Add tasks.");
		expect(text).not.toMatch(/Holm|alpha|sign-flip|p-value/i);
	});
	it("names the leader when the setups can be told apart", () => {
		expect(plainSetupsNote({ leader: "C", separable: true, pairs: [pair(40)] }, t)).toContain("C and the setups in its group");
	});
});

describe("costHeadline", () => {
	it("leads with the mean, else a known median, never Not known above a number", () => {
		expect(costHeadline(0.1, 0.09)).toBe("mean");
		expect(costHeadline(null, 0.0052)).toBe("median");
		expect(costHeadline(null, null)).toBe("none");
		expect(t("summary.medianCost")).toBe("Median cost per run");
	});
});