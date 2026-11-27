import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { guardTasks, joinSetups, plainInvalidReason } from "./verdict.utils";

// Fix wave 3, N200 point 6: the headline of a broken test names the reason in plain words.
describe("plainInvalidReason", () => {
	it.each([
		[
			"gate model_fidelity fired in 3 of 3 runs (100.0%) of arm B, above 5%",
			"the gateway used a different model in 3 of 3 runs of setup B",
		],
		["graded coverage 0.000 (0 of 3 runs) in arm A on clean is below 0.90", "only 0 of 3 runs of setup A were graded"],
		["gate egress fired in 2 of 12 runs of arm B", "the egress check fired in 2 of 12 runs"],
		["evidence: seals.log chain does not verify", "evidence: seals.log chain does not verify"],
	])("%s", (reason, want) => {
		expect(plainInvalidReason(reason)).toBe(want);
	});
});

describe("joinSetups and guardTasks", () => {
	it("joins setup letters for a headline", () => {
		expect(joinSetups(["A"])).toBe("A");
		expect(joinSetups(["A", "B"])).toBe("A and B");
		expect(joinSetups(["A", "B", "C"])).toBe("A, B and C");
	});
	it("reads the tasks of a failed integrity guard", () => {
		expect(guardTasks("veto gate fired in arm B on t02, t05 where arm A has none")).toBe("t02, t05");
		expect(guardTasks("clean regressed at both bounds")).toBeNull();
	});
});