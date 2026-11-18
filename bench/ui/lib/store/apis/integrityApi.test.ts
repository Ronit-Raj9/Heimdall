import { describe, expect, it } from "vitest";
import { BENCH_API_BASE } from "./fixtures";
import { compliancePackUrl, monitorReportUrl } from "./integrityApi";

describe("integrity URLs", () => {
	it("builds the compliance pack download for each format", () => {
		expect(compliancePackUrl("e_pilot", "md")).toBe(`${BENCH_API_BASE}/experiments/e_pilot/compliance?format=md`);
		expect(compliancePackUrl("e_pilot", "json")).toBe(`${BENCH_API_BASE}/experiments/e_pilot/compliance?format=json`);
	});
	it("escapes ids", () => {
		expect(compliancePackUrl("e/../x", "md")).toContain("e%2F..%2Fx");
		expect(monitorReportUrl("mc_1 2")).toBe(`${BENCH_API_BASE}/monitors/certifications/mc_1%202/report.md`);
	});
});