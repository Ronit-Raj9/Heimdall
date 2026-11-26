import { describe, expect, it } from "vitest";
import { liveSetupStartsJob } from "./liveActions";

// p13-9: "Decide later" writes no settings file, so it starts no job.
describe("live scoring set-up", () => {
	it("Decide later starts no job", () => {
		expect(liveSetupStartsJob("later")).toBe(false);
		expect(liveSetupStartsJob("keys")).toBe(true);
		expect(liveSetupStartsJob("tag")).toBe(true);
	});
});