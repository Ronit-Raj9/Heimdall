import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Report } from "@/lib/store/apis/evaluationsApi";
import { hasRedTeam, RedTeamSection } from "./redTeamSection";
import { scoreReport } from "./reportCard.fixtures";

const row = (id: string, label: string, attacks: number, broken: number) => ({ id, label, attacks, broken, success_rate: broken / attacks, ci_low: 0, ci_high: 1 });

const report: Report = {
	...scoreReport,
	red_team: {
		benchmarks: ["red-team"],
		arms: {
			A: {
				attacks: 4,
				broken: 1,
				success_rate: 0.25,
				ci_low: 0.05,
				ci_high: 0.7,
				median_attempts_to_break: 3,
				fewest_attempts_to_break: 3,
				by_category: [row("prompt_leak", "Instruction leak", 2, 1), row("policy_bypass", "Policy bypass", 2, 0)],
				by_strategy: [row("pair", "PAIR", 4, 1)],
				by_channel: [row("chat", "Chat", 4, 1)],
				hardest: { task: "rt.prompt_leak.pair.chat", run: "run-9", category: "prompt_leak", strategy: "pair", channel: "chat", summary: "Instruction leak by PAIR through Chat", attempts_to_break: 3 },
				most_effective: row("prompt_leak/pair", "Instruction leak by PAIR", 2, 1),
				regressions: 1,
			},
		},
	},
};

describe("red-team section", () => {
	it("shows the rate, attempts to first break and the hardest attack", () => {
		expect(hasRedTeam(report)).toBe(true);
		expect(hasRedTeam(scoreReport)).toBe(false);
		const html = renderToStaticMarkup(<RedTeamSection report={report} />);
		expect(html).toContain("25%");
		expect(html).toContain("3 attempts");
		expect(html).toContain("Instruction leak by PAIR through Chat");
		expect(html).toContain("run-9");
		expect(html).toContain("Policy bypass");
		expect(html).toContain("replayed earlier successful attacks");
		expect(html).not.toMatch(/—|–/);
	});

	it("says when nothing got through", () => {
		const held: Report = {
			...report,
			red_team: { benchmarks: ["red-team"], arms: { A: { ...report.red_team!.arms.A, broken: 0, success_rate: 0, median_attempts_to_break: null, fewest_attempts_to_break: null, hardest: undefined, most_effective: undefined, regressions: 0 } } },
		};
		const html = renderToStaticMarkup(<RedTeamSection report={held} />);
		expect(html).toContain("Never broken");
		expect(html).toContain("The agent held against every attack");
	});
});
