import { describe, expect, it } from "vitest";
import { packBenchmarks, workflowFileName } from "./pullRequestsTab";

describe("pull requests tab (M2-65, M2-66)", () => {
	it("lists the picked pack's benchmarks", () => {
		const packs = [{ id: "p1", benchmarks: ["a", "b"] }];
		expect(packBenchmarks(packs, "p1")).toEqual(["a", "b"]);
		expect(packBenchmarks(packs, "none")).toEqual([]);
		expect(packBenchmarks(undefined, "p1")).toEqual([]);
	});

	it("names the downloaded file after its repository path", () => {
		expect(workflowFileName(".github/workflows/heimdall.yml")).toBe("heimdall.yml");
		expect(workflowFileName(undefined)).toBe("heimdall.yml");
	});
});
