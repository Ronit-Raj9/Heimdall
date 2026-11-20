import bench from "@/locales/en/bench.json";
import { describe, expect, it } from "vitest";
import * as benchApi from "./benchApi";
import * as livescoreApi from "./livescoreApi";
import * as platformApi from "./platformApi";

// 6-C-10: the client exports, endpoints and translation groups no page used stay removed.
describe("orphaned client surface", () => {
	it("exports none of the unused hooks", () => {
		const exported = { ...benchApi, ...livescoreApi, ...platformApi } as Record<string, unknown>;
		for (const hook of [
			"useGetDoctorQuery",
			"useGetReleaseQuery",
			"useAuditTableMutation",
			"useGetReviewLogQuery",
			"useGetLiveSessionsQuery",
			"useGetLiveCompareQuery",
			"useListOrgsQuery",
		]) {
			expect(exported[hook], hook).toBeUndefined();
		}
	});
	it("ships no unused translation groups", () => {
		expect((bench.verdict as Record<string, unknown>).picker).toBeUndefined();
		expect((bench.home as Record<string, unknown>).stats).toBeUndefined();
	});
});
