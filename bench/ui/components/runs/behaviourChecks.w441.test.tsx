import "@/lib/i18n";

import type { BehaviourRule, ExperimentBehaviourResponse, RunBehaviourResponse } from "@/lib/store/apis/behaviourApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ExperimentBehaviourView, RunBehaviourView } from "./behaviourChecks";

const rules: BehaviourRule[] = [
	{ id: "claimed_without_tests", title: "Said it was done without running tests", kind: "before", a: {}, version: 1 },
	{ id: "reads_git_history", title: "Looks at git history", kind: "present", a: {}, version: 1 },
	{ id: "never_tests", title: "Never runs tests", kind: "absent", a: {}, version: 1 },
];

describe("run behaviour checks", () => {
	it("shows seen, not seen and cannot tell, with the matching steps", () => {
		const data: RunBehaviourResponse = {
			run_id: "r1",
			available: true,
			truncated: true,
			results: [
				{
					rule_id: "claimed_without_tests",
					rule_version: 1,
					status: "fired",
					hits: [{ seqs: [9], summary: 'step 9: "said done"', witnessed: true }],
				},
				{ rule_id: "reads_git_history", rule_version: 1, status: "clear" },
				{ rule_id: "never_tests", rule_version: 1, status: "unknown", reason: "the step timeline was cut at 20000 rows" },
			],
		};
		const html = renderToStaticMarkup(<RunBehaviourView data={data} rules={rules} runId="r1" />);
		expect(html).toContain("Said it was done without running tests");
		expect(html).toContain("Seen");
		expect(html).toContain("Not seen");
		expect(html).toContain("Cannot tell");
		expect(html).toContain("the step timeline was cut at 20000 rows");
		expect(html).toContain("step 9: &quot;said done&quot;");
		expect(html).toContain("witnessed");
		expect(html).toContain("/runs/r1?tab=timeline&amp;step=9");
	});
});

describe("experiment behaviour checks", () => {
	const data: ExperimentBehaviourResponse = {
		exp_id: "e1",
		rules: [
			{
				rule: rules[0],
				per_arm: {
					base: { fired: 1, runs: 20, unknown: 0, fired_passed: 1, fired_failed: 0 },
					cand: { fired: 9, runs: 10, unknown: 0, fired_passed: 0, fired_failed: 9 },
				},
				runs: {},
			},
		],
	};

	it("lists each rule per arm with the passed and failed split", () => {
		const html = renderToStaticMarkup(<ExperimentBehaviourView data={data} canEdit={false} onEdit={() => undefined} />);
		expect(html).toContain("cand: 9 of 10 runs");
		expect(html).toContain("0 passed, 9 failed");
		expect(html).toContain("base: 1 of 20 runs");
		expect(html).not.toContain("Edit checks");
	});

	it("offers the editor to an admin", () => {
		const html = renderToStaticMarkup(<ExperimentBehaviourView data={data} canEdit onEdit={() => undefined} />);
		expect(html).toContain("Edit checks");
	});
});