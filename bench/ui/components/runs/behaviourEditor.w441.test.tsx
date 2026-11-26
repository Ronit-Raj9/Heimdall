import "@/lib/i18n";

import type { BehaviourRule } from "@/lib/store/apis/behaviourApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BehaviourEditor, cleanRule, newRuleId } from "./behaviourEditor";

const rule: BehaviourRule = { id: "no_force_push", title: "Force-pushes", kind: "present", a: { command: "git push --force" }, version: 2 };

describe("behaviour check editor", () => {
	it("shows the user rules with plain field names and every kind", () => {
		const html = renderToStaticMarkup(
			<BehaviourEditor initial={[rule]} saving={false} onSave={() => undefined} onCancel={() => undefined} />,
		);
		for (const text of [
			"Title",
			"What to look for",
			"A step happens",
			"A step never happens",
			"A step happens without a later step",
			"A step happens before another",
			"The same step repeats",
			"An error is ignored",
			"Command matches",
			"Tool name",
			"File path",
			"Only steps we saw happen",
			"Save checks",
		]) {
			expect(html).toContain(text);
		}
		expect(html).toContain('value="Force-pushes"');
	});

	it("drops empty fields and the second step of kinds that have none", () => {
		const out = cleanRule({ ...rule, a: { command: "x", tool: "", path: "" }, b: { tool: "bash" } });
		expect(out.a).toEqual({ command: "x" });
		expect(out.b).toBeUndefined();
		const seq = cleanRule({ ...rule, kind: "sequence", b: { tool: "", command: "pytest" } });
		expect(seq.b).toEqual({ command: "pytest" });
	});

	it("makes a new id that is not taken", () => {
		expect(newRuleId([{ ...rule, id: "my_check_1" }])).toBe("my_check_2");
	});
});