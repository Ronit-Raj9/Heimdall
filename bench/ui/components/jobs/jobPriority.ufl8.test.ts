import { jobPriorityRequest } from "@/lib/store/apis/jobPriorityApi";
import { describe, expect, it } from "vitest";
import { PRIORITY_STEPS } from "./jobPriority";

describe("job priority", () => {
	it("posts the body handlers/jobpriority.go reads", () => {
		// Same literal as TestJobPriorityRoute "bumped" (handlers/jobpriority_test.go).
		const r = jobPriorityRequest("job_1", 5);
		expect(r).toMatchObject({ url: "/jobs/job_1/priority", method: "POST" });
		expect(JSON.stringify(r.body)).toBe('{"priority":5}');
	});
	it("offers only values the server accepts", () => {
		for (const p of PRIORITY_STEPS) expect(p >= -10 && p <= 10).toBe(true);
	});
});