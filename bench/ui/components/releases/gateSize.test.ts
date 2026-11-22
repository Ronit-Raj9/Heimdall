import { describe, expect, it } from "vitest";
import { mde, tasksFor } from "./gateSize";

describe("gate size", () => {
	it("matches the server's smallest detectable change", () => {
		// stats.MDE: 0.346 at 8 tasks, about 0.20 at 20, 0.10 at 73, 0.05 at 285 (audit G4 section 3.3).
		expect(mde(8)).toBeCloseTo(0.346, 1);
		expect(Math.round(mde(20) * 100)).toBe(20);
		expect(Math.round(mde(73) * 100)).toBe(10);
		expect(mde(1)).toBe(Infinity);
	});

	it("finds the tasks a limit needs", () => {
		const n = tasksFor(0.05);
		expect(n).toBeGreaterThan(270);
		expect(n).toBeLessThan(300);
		expect(tasksFor(0)).toBe(0);
		expect(tasksFor(0.001, 1000)).toBe(0);
	});
});
