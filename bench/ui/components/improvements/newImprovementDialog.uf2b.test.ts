import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { defaultImprovementName, IMPROVEMENT_NAME, suiteChoices } from "./newImprovementDialog";

// Fix wave lane 2b: the improvement form's default name and benchmark options.
describe("new improvement helpers (fix wave 2b)", () => {
	it("suggests <agent>-improve-<yyyymmdd>, valid for the server", () => {
		const name = defaultImprovementName("acme-agent", new Date(2026, 10, 27));
		expect(name).toBe("acme-agent-improve-20261127");
		const long = defaultImprovementName("a".repeat(80), new Date(2026, 0, 2));
		expect(long.length).toBeLessThanOrEqual(63);
		expect(IMPROVEMENT_NAME.test(long)).toBe(true);
	});

	it("shows the benchmark and its task count, the version only when two exist, not-ready ones apart", () => {
		const out = suiteChoices([
			{ id: "tb2", version: "2.0.0", task_count: 12 },
			{ id: "bfcl", version: "1.0.0", task_count: 5 },
			{ id: "bfcl", version: "1.1.0", task_count: 6 },
			{ id: "empty", version: "1.0.0", task_count: 3, ready_known: true, ready_task_ids: [] },
		]);
		expect(out.map((c) => c.value)).toEqual(["bfcl@1.0.0", "bfcl@1.1.0", "empty@1.0.0", "tb2@2.0.0"]);
		expect(out[0].label).toBe("bfcl 1.0.0 (5 tasks)");
		expect(out[3].label).toBe("tb2 (12 tasks)");
		expect(out[2].notReady).toBe(true);
		expect(out[3].notReady).toBe(false);
	});
});
