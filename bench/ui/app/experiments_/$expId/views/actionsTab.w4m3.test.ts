import "@/lib/i18n";

import { describe, expect, it, vi } from "vitest";
import { extendParams } from "./actionsTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("extendParams (p06-2)", () => {
	it("gives no job for 0 or a negative count", () => {
		expect(extendParams("0", 10, "trials", "e_1", false)).toBeNull();
		expect(extendParams("-5", 10, "trials", "e_1", false)).toBeNull();
		expect(extendParams("", 10, "trials", "e_1", false)).toBeNull();
		expect(extendParams("11", 10, "tasks", "e_1", false)).toBeNull();
	});
	it("keeps the count that was typed", () => {
		expect(extendParams("3", 10, "trials", "e_1", false)).toEqual({ exp_id: "e_1", trials: 3 });
		expect(extendParams("3", 10, "tasks", "e_1", true)).toEqual({ exp_id: "e_1", tasks: 3, force: true });
	});
});