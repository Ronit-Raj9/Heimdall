import { monitorUpdateRequest } from "@/lib/store/apis/liveMonitorEditApi";
import { describe, expect, it } from "vitest";
import { editFieldsOf, monitorUpdateBody } from "./editLiveMonitor";

const d = { name: "prod bot", tasks: ["t1", "t2", "t3"], interval_min: 60, budget_usd_per_day: 6, rule: { window: 6, min_drop: 0.1 } };

describe("edit live monitor", () => {
	const before = editFieldsOf(d, 3);

	it("sends only what changed, the rule whole, as handlers/livemonitors_ufl8_test.go posts it", () => {
		const body = monitorUpdateBody(d, before, { ...before, interval: "30", minDrop: "20", floor: "50" }, 15);
		// Same literal as TestLiveMonitorEditFromWebBody.
		expect(JSON.stringify(monitorUpdateRequest("m1", body!).body)).toBe(
			'{"interval_min":30,"rule":{"window":6,"min_drop":0.2,"min_pass_rate":0.5}}',
		);
		expect(monitorUpdateRequest("m1", body!)).toMatchObject({ url: "/live-monitors/m1", method: "PUT" });
	});

	it("sends nothing when nothing changed and refuses out of range numbers", () => {
		expect(monitorUpdateBody(d, before, before, 15)).toEqual({});
		expect(monitorUpdateBody(d, before, { ...before, interval: "5" }, 15)).toBeNull();
		expect(monitorUpdateBody(d, before, { ...before, floor: "150" }, 15)).toBeNull();
	});
});