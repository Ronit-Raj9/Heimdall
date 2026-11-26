import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { describe, expect, it, vi } from "vitest";
import { benchmarksNote, setupLine } from "./evaluationView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("evaluation page notes (w4m2)", () => {
	it("says the benchmarks are starting while nothing has run or been skipped", () => {
		expect(benchmarksNote(0, 0, true)).toBe("starting");
		expect(benchmarksNote(0, 1, false)).toBe("none");
		expect(benchmarksNote(0, 1, true)).toBe("none");
		expect(benchmarksNote(0, 0, false)).toBe("none");
		expect(benchmarksNote(2, 0, true)).toBeNull();
	});
	it("builds the setup line with plurals", () => {
		const t = i18n.getFixedT("en") as unknown as (k: string, o?: Record<string, unknown>) => string;
		expect(setupLine(t, 3, 1)).toBe("3 tasks per benchmark, each tried once");
		expect(setupLine(t, 1, 3)).toBe("1 task per benchmark, each tried 3 times");
	});
});