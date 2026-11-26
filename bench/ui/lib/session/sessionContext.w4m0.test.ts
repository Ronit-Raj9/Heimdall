import { describe, expect, it } from "vitest";
import { sessionFromMe } from "./sessionContext";

// Wave 4, p15-7: a busy server (429) is told apart from one that cannot be reached.
describe("sessionFromMe busy", () => {
	it("marks a 429 as busy", () => {
		const s = sessionFromMe({ meError: { status: 429, data: {} }, meLoading: false });
		expect(s.status).toBe("error");
		expect(s.busy).toBe(true);
	});
	it("does not mark a 503 as busy", () => {
		const s = sessionFromMe({ meError: { status: 503, data: {} }, meLoading: false });
		expect(s.status).toBe("error");
		expect(s.busy).toBe(false);
	});
});