import { describe, expect, it } from "vitest";
import { parseSkillRefusal } from "./skillScanProblem";

describe("parseSkillRefusal (w4m2)", () => {
	it("reads the gate sentence and its plain form to the same skill and agent", () => {
		const gate = parseSkillRefusal(
			"plan experiment: skill zz-good in agents/zz-test-loop/skills has no passing scan: open the agent's Tools tab",
		);
		const plain = parseSkillRefusal(
			"Skill zz-good of agent zz-test-loop has no passing safety scan. Open the agent's Tools tab and press Scan.",
		);
		expect(gate).toEqual({ skill: "zz-good", agent: "zz-test-loop", changed: false });
		expect(plain).toEqual(gate);
	});
	it("reads the changed forms", () => {
		const gate = parseSkillRefusal("skill a in agents/b/skills changed after its scan: rescan");
		const plain = parseSkillRefusal(
			"Cannot estimate this benchmark: Skill a of agent b changed after its safety scan. Open the agent's Tools tab and press Scan.",
		);
		expect(gate).toEqual({ skill: "a", agent: "b", changed: true });
		expect(plain).toEqual(gate);
	});
	it("returns null for other text", () => {
		expect(parseSkillRefusal("The benchmark has no lock.")).toBeNull();
	});
});