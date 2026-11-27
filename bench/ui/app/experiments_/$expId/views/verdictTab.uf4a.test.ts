import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

import { slowStage } from "./verdictTab";

describe("slowStage (M4-5)", () => {
	it("waits quietly, says it is still working at 8 s and fails at 30 s", () => {
		expect(slowStage(0)).toBe("wait");
		expect(slowStage(7_999)).toBe("wait");
		expect(slowStage(8_000)).toBe("slow");
		expect(slowStage(29_999)).toBe("slow");
		expect(slowStage(30_000)).toBe("stuck");
	});
});