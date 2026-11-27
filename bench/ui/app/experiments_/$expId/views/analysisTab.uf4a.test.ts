import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

import { ANALYSIS_JOBS, visibleAnalysisJobs } from "./analysisTab";

describe("visibleAnalysisJobs (M4-35)", () => {
	it("shows the maintenance jobs to admins only", () => {
		expect(visibleAnalysisJobs(true)).toEqual(ANALYSIS_JOBS);
		expect(visibleAnalysisJobs(false).map((s) => s.key)).toEqual(["codescan", "awareness", "blame"]);
	});
});