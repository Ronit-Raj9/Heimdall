import { describe, expect, it } from "vitest";
import { legacyExperimentTarget } from "@/components/experiments/legacyRoutes";
import { migrationParams } from "./migrationTab";

// 6-C-09: a migration bookmark's alpha and grade_source reach the request, not only the address bar.
describe("migrationParams", () => {
	it("forwards alpha and grade_source with the arm selection", () => {
		expect(migrationParams("e1", { from: "A", to: "B", condition: null, alpha: "0.1", grade_source: "reviewed" })).toEqual({
			expId: "e1",
			from: "A",
			to: "B",
			alpha: "0.1",
			grade_source: "reviewed",
		});
	});
	it("keeps the legacy redirect's alpha and grade_source through to the request", () => {
		const target = legacyExperimentTarget("/migration", { exp: "e1", alpha: 0.01, grade_source: "reviewed" });
		expect(migrationParams("e1", target.search ?? {})).toMatchObject({ alpha: "0.01", grade_source: "reviewed" });
	});
	it("forwards the chosen release rule set (N280)", () => {
		expect(migrationParams("e1", { policy: "none" })).toEqual({ expId: "e1", policy: "none" });
	});
	it("leaves defaults to the server", () => {
		expect(migrationParams("e1", {})).toEqual({ expId: "e1" });
	});
});
