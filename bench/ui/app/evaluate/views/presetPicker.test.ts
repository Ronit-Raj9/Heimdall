import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { slotsText } from "./presetPicker";

describe("slotsText (N90)", () => {
	it("keeps the org line when the worker is not known", () => {
		expect(slotsText({ slots: { concurrency: 4, org_slots: 4, parallel: 4 } })).toBe(
			"Up to 4 runs at once (4 per benchmark, your organization has 4 worker slots).",
		);
		expect(slotsText({ slots: { concurrency: 1, org_slots: 0, parallel: 1 } })).toBe("Up to 1 run at once (1 per benchmark).");
		expect(slotsText({})).toBeUndefined();
	});

	it("names the worker's runs at a time when the estimate knows them", () => {
		expect(slotsText({ slots: { concurrency: 2, org_slots: 4, parallel: 2, worker_slots: 2 } })).toBe(
			"Up to 2 runs in parallel. Its worker runs 2 at a time.",
		);
	});
});