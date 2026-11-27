import "@/lib/i18n";

import type { RuleResult, RunBehaviourResponse } from "@/lib/store/apis/behaviourApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { behaviourOrder, RunBehaviourView } from "./behaviourChecks";
import { ruleComplete } from "./behaviourEditor";

const r = (rule_id: string, status: RuleResult["status"]): RuleResult => ({ rule_id, rule_version: 1, status });

describe("behaviour checks on a run (M5-4)", () => {
	it("orders fired first, then cannot tell, then clear", () => {
		const o = behaviourOrder([r("a", "clear"), r("b", "fired"), r("c", "unknown")]);
		expect(o.fired.map((x) => x.rule_id)).toEqual(["b"]);
		expect(o.unknown.map((x) => x.rule_id)).toEqual(["c"]);
		expect(o.clear.map((x) => x.rule_id)).toEqual(["a"]);
		expect(o.allUnknown).toBe(false);
	});

	it("says once that the steps could not be read when every check is unknown", () => {
		const data: RunBehaviourResponse = { run_id: "r1", available: false, results: [r("a", "unknown"), r("b", "unknown")] } as RunBehaviourResponse;
		const html = renderToStaticMarkup(<RunBehaviourView data={data} rules={[]} runId="r1" />);
		expect(html).toContain("Checks could not read this run&#x27;s steps.");
		expect(html).not.toContain("behaviour-run-a");
	});
});

describe("behaviour editor (M5-25)", () => {
	it("needs a name and a step to match", () => {
		expect(ruleComplete({ id: "x", title: " ", kind: "present", a: { tool: "bash" }, version: 1 })).toBe(false);
		expect(ruleComplete({ id: "x", title: "Runs bash", kind: "present", a: {}, version: 1 })).toBe(false);
		expect(ruleComplete({ id: "x", title: "Runs bash", kind: "present", a: { tool: "bash" }, version: 1 })).toBe(true);
	});
});
