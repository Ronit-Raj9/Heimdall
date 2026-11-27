import { describe, expect, it } from "vitest";
import { webhookFormProblems, webhookUrlProblem } from "./integrationsView";

describe("hook form reasons (M7-70)", () => {
	it("refuses local addresses on the hosted server", () => {
		expect(webhookUrlProblem("https://localhost:8080/x", true)).toBe("local");
		expect(webhookUrlProblem("https://192.168.1.4/hook", true)).toBe("local");
		expect(webhookUrlProblem("https://hooks.slack.com/x", true)).toBeNull();
		expect(webhookUrlProblem("http://localhost", false)).toBeNull();
	});
	it("says why a hook cannot be added", () => {
		expect(webhookFormProblems("pagerduty", "short", ["evaluation.regressed"])).toEqual(["integrations.badRoutingKey"]);
		expect(webhookFormProblems("slack", "", [])).toEqual(["integrations.noEvents"]);
		expect(webhookFormProblems("slack", "", ["evaluation.finished"])).toEqual([]);
	});
});
