import "@/lib/i18n";

import { describe, expect, it, vi } from "vitest";
import { showPromote } from "./verdictTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("showPromote (p05-4)", () => {
	it("hides the Use model advice while the comparison runs", () => {
		expect(showPromote(true)).toBe(false);
		expect(showPromote(false)).toBe(true);
	});
});