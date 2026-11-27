import { describe, expect, it } from "vitest";
import { jobKindLabel, targetLabel } from "./jobKindLabels";

describe("job names (M8-58)", () => {
	it("names the onboarding jobs", () => {
		expect(jobKindLabel("onboard.ready")).toBe("Check a new benchmark");
		expect(jobKindLabel("onboard.publish")).toBe("Publish a benchmark");
	});
	it("drops kind prefixes from targets and the bare index target", () => {
		expect(targetLabel("onboard:zz-test-p10-csv")).toBe("zz-test-p10-csv");
		expect(targetLabel("exp:e_eval_1")).toBe("e_eval_1");
		expect(targetLabel("suite:web")).toBe("web");
		expect(targetLabel("canary:c1")).toBe("c1");
		expect(targetLabel("index")).toBe("");
		expect(targetLabel(undefined)).toBe("");
		expect(targetLabel("agent-7")).toBe("agent-7");
	});
});
