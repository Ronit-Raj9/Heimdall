import "@/lib/i18n";

import i18n from "i18next";
import { describe, expect, it } from "vitest";
import type { ScheduleRow } from "@/lib/types/schedules";
import { repeatCost } from "./repeatCost";
import { repeatConfirmText, runNowMessage, type RowF2 } from "./schedulesView";

// Lane F2: a new repeat run confirms with its cost a run, a day and a month (PH5-10); Run now on a
// model watch says it checks the catalog, not that it restarts an evaluation (PT-4).
const t = i18n.getFixedT("en", "schedules");

describe("repeat run confirms (lane F2)", () => {
	it("names the cost of one run, the day and the month", () => {
		const text = repeatConfirmText(repeatCost(0.005, 0.4, 1));
		expect(text).toContain("$0.40 a day");
		expect(text).toContain("$12");
		expect(text).toMatch(/One run costs about \$0\.0050?/);
		expect(repeatConfirmText(repeatCost(null, 0.4, 1))).toContain("$0.40 a day");
	});

	it("Run now on a model watch does not say it restarts the evaluation", () => {
		const row = {
			name: "new-models-x", evaluation: "ev_20261127_214713_d1333f", cron: "0 6 * * *", kind: "evaluation", source: "web",
			spent_today_usd: 0, max_usd_per_day: 0.5, new_models: { providers: ["openai"] },
		} as RowF2 as ScheduleRow;
		const text = runNowMessage(t, row);
		expect(text).not.toContain("restarts");
		expect(text).toContain("model catalog");
		expect(text).toContain("0.50");
	});
});
