import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { PRESETS, PresetPicker } from "./presetPicker";

describe("PresetPicker", () => {
	it("offers Small, Medium, Large and Custom with the A3.6 sizes, the picked one checked", () => {
		const html = render(<PresetPicker value="medium" onChange={() => undefined} />);
		for (const label of ["Small", "Medium", "Large", "Custom"]) expect(html).toContain(label);
		// PAID-19: a named size uses every ready task, so it no longer claims "about 60 tasks".
		expect(html).toContain("5 rounds, 4 ideas each, every ready task");
		expect(html).not.toContain("about 60 tasks");
		expect(html).toMatch(/<input[^>]*checked=""[^>]*value="medium"|<input[^>]*value="medium"[^>]*checked=""/);
		expect(PRESETS.small).toMatchObject({ rounds: 3, ideas: 2, limit: 15 });
		expect(PRESETS.large).toMatchObject({ rounds: 8, ideas: 8, limit: 400 });
	});

	it("offers no switch to turn a preset part off", () => {
		const html = render(<PresetPicker value="small" onChange={() => undefined} />);
		expect(html).not.toMatch(/type="checkbox"/);
	});
});