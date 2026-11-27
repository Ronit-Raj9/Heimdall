import { describe, expect, it } from "vitest";
import { saveNextVersion } from "./configTab";

// M6-77: Save says which version it makes.
describe("saveNextVersion", () => {
	it("is the newest saved version plus one", () => {
		expect(saveNextVersion([{ version: 1 }, { version: 3 }, { version: 2 }])).toBe(4);
	});
	it("is 1 for an agent with no version yet", () => {
		expect(saveNextVersion([])).toBe(1);
	});
	it("is null while the versions are unknown", () => {
		expect(saveNextVersion(undefined)).toBeNull();
	});
});
