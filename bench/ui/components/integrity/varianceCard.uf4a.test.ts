import { describe, expect, it } from "vitest";
import { noiseSentence } from "./varianceCard";

const t = (key: string, o?: Record<string, unknown>) => `${key}:${JSON.stringify(o ?? {})}`;

// M4-42, M4-15: the noise floor as one plain sentence.
describe("noiseSentence", () => {
	it("names the tasks, the smallest visible gap and the real difference", () => {
		const line = noiseSentence(t, { mde: 34.6, effect: { n_tasks: 8, d_bar: 0 } as never, variance: undefined });
		expect(line).toBe('noise.floorTasks:{"count":8,"points":35} noise.real:{"points":0}');
	});
	it("says nothing without a floor", () => {
		expect(noiseSentence(t, { mde: 0, effect: undefined, variance: undefined })).toBeNull();
	});
	it("prefers the variance card's MDE", () => {
		expect(noiseSentence(t, { mde: 10, effect: undefined, variance: { mde: 20 } as never })).toBe('noise.floor:{"points":20}');
	});
});