import "@/lib/i18n";
import i18n from "i18next";
import { describe, expect, it } from "vitest";
import { repeatCost, repeatCostText } from "./repeatCost";
import "./ufrjI18n";

describe("repeat run cost, one definition per number (R6-3)", () => {
	it("keeps the run cost exact and works the month out from the daily limit", () => {
		const c = repeatCost(0.005, 0.01, 1);
		expect(c).toEqual({ perRun: "$0.0050", dailyLimit: "$0.01", monthMax: "$0.30", runsADay: 1 });
		const { key, values } = repeatCostText(c);
		expect(i18n.t(key, { ns: "ufrj", ...values })).toBe(
			"One run costs about $0.0050. Daily limit $0.01, at most 1 run a day. Monthly max $0.30.",
		);
	});
	it("lets the limit stop extra runs and says when the run cost is not known", () => {
		expect(repeatCost(0.005, 0.01, 24).runsADay).toBe(2);
		expect(repeatCostText(repeatCost(undefined, 3)).key).toBe("repeat.cost.unknownRun");
		expect(repeatCost(undefined, 3).monthMax).toBe("$90");
	});
});
