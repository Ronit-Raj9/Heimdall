import { describe, expect, it } from "vitest";
import { bucketLabel, presetOf, presetRange, tableRows, usageCsv, usageCsvName } from "./usageView";

const now = new Date("2026-11-27T09:00:00Z");
const b = (key: string, cost: number, runs: number) => ({ key, cost_usd: cost, tokens: runs * 10, runs, unknown_cost_runs: 0 });

describe("usage periods (lane 8a)", () => {
	it("computes the presets in UTC", () => {
		expect(presetRange("month", now)).toEqual({ from: "2026-11-01", to: "2026-11-27" });
		expect(presetRange("lastMonth", now)).toEqual({ from: "2026-10-01", to: "2026-10-31" });
		expect(presetRange("7d", now)).toEqual({ from: "2026-11-21", to: "2026-11-27" });
		expect(presetRange("lastMonth", new Date("2026-01-05T00:00:00Z"))).toEqual({ from: "2025-12-01", to: "2025-12-31" });
	});
	it("names the preset of a range, none for a custom one", () => {
		expect(presetOf({ from: "2026-11-01", to: "2026-11-27" }, now)).toBe("month");
		expect(presetOf({ from: "2026-11-02", to: "2026-11-27" }, now)).toBeUndefined();
	});
});

describe("usage table rows", () => {
	const buckets = [b("2026-11-01", 0, 0), b("2026-11-02", 0.5, 2), b("2026-11-03", 0.1, 1)];
	it("hides empty days by default, newest first", () => {
		expect(tableRows(buckets, "day", false).map((r) => r.key)).toEqual(["2026-11-03", "2026-11-02"]);
		expect(tableRows(buckets, "day", true)).toHaveLength(3);
	});
	it("sorts models by cost, highest first", () => {
		expect(tableRows([b("a", 0.1, 1), b("c", 0.9, 1)], "model", false).map((r) => r.key)).toEqual(["c", "a"]);
	});
	it("writes day keys as words", () => {
		expect(bucketLabel("2026-11-10", "en-GB")).toBe("Tue 10 Nov");
		expect(bucketLabel("gpt-4o")).toBe("gpt-4o");
	});
});

describe("usage CSV", () => {
	it("has the range and definition, then one row per bucket", () => {
		const data = {
			from: "2026-11-01",
			to: "2026-11-27",
			group_by: "model" as const,
			buckets: [{ ...b("gpt, mini", 0.5, 2), label: 'say "hi"' }],
		};
		const lines = usageCsv(data, "Spend: priced.").trim().split("\n");
		expect(lines[0]).toContain("2026-11-01 to 2026-11-27");
		expect(lines[0]).toContain("Spend: priced.");
		expect(lines[1]).toBe("group,label,cost_usd,tokens,runs,unknown_cost_runs");
		expect(lines[2]).toBe('"gpt, mini","say ""hi""",0.5,20,2,0');
		expect(usageCsvName(data)).toBe("heimdall-usage-2026-11-01-2026-11-27-model.csv");
	});
});