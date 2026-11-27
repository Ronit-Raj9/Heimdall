import "@/lib/i18n";

import type { RuleResult, RunBehaviourResponse } from "@/lib/store/apis/behaviourApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { behaviourOrder, ExperimentBehaviourView, noRunsChecked, RunBehaviourView, sharedUnknownReason } from "./behaviourChecks";
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
		expect(html).toContain("This run recorded no steps the checks can read, so none of them could tell.");
		expect(html).not.toContain("behaviour-run-a");
	});
});

describe("behaviour checks reasons and counts (M5-4, M7-44)", () => {
	const u = (rule_id: string, reason?: string): RuleResult => ({ ...r(rule_id, "unknown"), reason });

	it("finds the one reason every cannot tell result shares", () => {
		expect(sharedUnknownReason([u("a", "no timeline"), u("b", "no timeline ")])).toBe("no timeline");
		expect(sharedUnknownReason([u("a", "no timeline"), u("b", "other")])).toBeUndefined();
		expect(sharedUnknownReason([u("a")])).toBeUndefined();
		expect(sharedUnknownReason([])).toBeUndefined();
	});

	it("says a shared reason once, not on every row", () => {
		const data = { run_id: "r1", available: true, results: [r("c", "clear"), u("a", "no timeline"), u("b", "no timeline")] } as RunBehaviourResponse;
		const html = renderToStaticMarkup(<RunBehaviourView data={data} rules={[]} runId="r1" />);
		expect(html.split("no timeline").length - 1).toBe(1);
	});

	it("reads no runs checked when every arm has zero runs", () => {
		expect(noRunsChecked({})).toBe(true);
		expect(noRunsChecked({ A: { runs: 0 } })).toBe(true);
		expect(noRunsChecked({ A: { runs: 0 }, B: { runs: 2 } })).toBe(false);
		const html = renderToStaticMarkup(
			<ExperimentBehaviourView
				data={{ rules: [{ rule: { id: "x", title: "X", kind: "present", a: {}, version: 1 }, per_arm: {} }] } as never}
				canEdit={false}
				onEdit={() => undefined}
			/>,
		);
		expect(html).toContain("No runs checked yet");
	});
});

describe("behaviour editor (M5-25)", () => {
	it("needs a name and a step to match", () => {
		expect(ruleComplete({ id: "x", title: " ", kind: "present", a: { tool: "bash" }, version: 1 })).toBe(false);
		expect(ruleComplete({ id: "x", title: "Runs bash", kind: "present", a: {}, version: 1 })).toBe(false);
		expect(ruleComplete({ id: "x", title: "Runs bash", kind: "present", a: { tool: "bash" }, version: 1 })).toBe(true);
	});
});
