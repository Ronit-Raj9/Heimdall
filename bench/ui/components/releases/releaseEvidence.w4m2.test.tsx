import "@/lib/i18n";

import { describe, expect, it } from "vitest";
import i18n from "@/lib/i18n";
import { releaseTitle, sharedTaskCount } from "./releaseEvidence";

const t = i18n.getFixedT("en", "u14") as unknown as (k: string, o?: Record<string, unknown>) => string;

describe("same-version release heading (w4m2)", () => {
	it("says the numbers cover only the tasks both evaluations ran", () => {
		const shared = sharedTaskCount({
			changed: false,
			summary: "",
			benchmarks: [{ benchmark: "b1", changed: false, compared: 3, new_tasks: 0, dropped_tasks: 0 }],
		} as never);
		expect(shared).toBe(3);
		const title = releaseTitle(
			{ agent_id: "codex", version: 4, compared_version: 4, agent_config_id: "", arm: "A", shared_tasks: shared },
			t,
		);
		expect(title).toContain("on the 3 tasks both ran");
		expect(
			releaseTitle({ agent_id: "codex", version: 4, compared_version: 4, agent_config_id: "", arm: "A", shared_tasks: 1 }, t),
		).toContain("on the one task both ran");
	});
});