import "@/lib/i18n";

import i18n from "@/lib/i18n";
import type { Evaluation } from "@/lib/store/apis/evaluationsApi";
import { describe, expect, it } from "vitest";
import { addScheduleMissing, alertEventLabel, finishedEvaluationGroups } from "./schedulesView";

const w = i18n.getFixedT("en", "w4m7b");
const m = i18n.getFixedT("en", "uf7b");

describe("repeat run form (M7-46, M7-52, M7-59)", () => {
	it("refuses a cron line that is not valid", () => {
		const s = { name: "n", max: "3" };
		expect(addScheduleMissing({ ...s, cron: "61 25 * *" }, "evaluation", "ev_1", w, "bad")).toEqual(["bad"]);
		expect(addScheduleMissing({ ...s, cron: "0 2 * * *" }, "evaluation", "ev_1", w, "bad")).toEqual([]);
	});
	it("lists finished evaluations only, grouped by agent", () => {
		const ev = (id: string, agent: string, status: Evaluation["status"], created: string, score?: number) =>
			({
				id,
				status,
				created_at: created,
				spec: { name: `${agent} score`, arms: [{ label: "A", agent, model: "m" }], benchmarks: [] },
				...(score != null ? { result: { arms: [{ score }], cost_usd: 0.2, at: "" } } : {}),
			}) as unknown as Evaluation;
		const groups = finishedEvaluationGroups([
			ev("ev_1", "codex", "done", "2026-11-22T10:00:00Z", 0.71),
			ev("ev_2", "codex", "failed", "2026-11-23T10:00:00Z"),
			ev("ev_3", "bot", "done", "2026-11-20T10:00:00Z", 0.5),
		]);
		expect(groups.map((g) => g.agent)).toEqual(["codex", "bot"]);
		expect(groups[0].options).toEqual([{ id: "ev_1", label: "codex score, 22 Nov, score 71%", cost: 0.2 }]);
	});
	it("names alert events in plain words", () => {
		expect(alertEventLabel("canary_drifted", m)).toBe("A drift monitor dropped");
		expect(alertEventLabel("schedule_failed", m)).toBe("A repeat run failed");
		expect(alertEventLabel("new_thing", m)).toBe("new thing");
	});
});
