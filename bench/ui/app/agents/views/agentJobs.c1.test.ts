import { describe, expect, it } from "vitest";
import { agentJobsPollMs } from "./agentJobs";

describe("agents page job polling (X-78)", () => {
	it("polls the job list only while a job runs or the count is unknown", () => {
		expect(agentJobsPollMs(0)).toBe(0);
		expect(agentJobsPollMs(2)).toBe(15000);
		expect(agentJobsPollMs(null)).toBe(15000);
		expect(agentJobsPollMs(undefined)).toBe(15000);
	});
});
