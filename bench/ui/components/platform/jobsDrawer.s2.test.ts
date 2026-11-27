import { describe, expect, it } from "vitest";
import { finishedSince } from "@/components/platform/jobsDrawer";
import type { Job } from "@/lib/types/bench";

const job = (id: string, status: string) => ({ id, status, kind: "suite.lint" }) as unknown as Job;

describe("finishedSince (M1-45 finish notices)", () => {
	it("reports nothing on the first poll", () => {
		expect(finishedSince(null, [job("a", "succeeded")])).toEqual([]);
	});
	it("reports jobs that left the active set", () => {
		const prev = new Set(["a", "b"]);
		const got = finishedSince(prev, [job("a", "succeeded"), job("b", "running"), job("c", "failed")]);
		expect(got.map((j) => j.id)).toEqual(["a"]);
	});
});
