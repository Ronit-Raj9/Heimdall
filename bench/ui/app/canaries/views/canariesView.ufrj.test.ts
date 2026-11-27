import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const { formOpensFromLink } = await import("./canariesView");

describe("Watch this agent for drift (R6-2)", () => {
	it("opens the new monitor form only when the link names an agent", () => {
		expect(formOpensFromLink("codex")).toBe(true);
		expect(formOpensFromLink(null)).toBe(false);
		expect(formOpensFromLink("")).toBe(false);
	});
});
