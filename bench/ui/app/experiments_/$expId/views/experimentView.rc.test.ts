import i18n from "@/lib/i18n";

import { describe, expect, it } from "vitest";
import { experimentSectionKey } from "./experimentView";

// Round 2 lane RC: the page header names and the browser tab title of a comparison.
describe("comparison section name (Q-4)", () => {
	const t = i18n.getFixedT("en", "bench");
	it("reads Comparisons, never the raw key", () => {
		expect(t(experimentSectionKey(undefined) as never)).toBe("Comparisons");
	});
	it("reads Evaluations for a benchmark of an evaluation", () => {
		expect(t(experimentSectionKey("ev_1") as never)).toBe("Evaluations");
	});
});

describe("the two header menus have different names (Q-41)", () => {
	it("names the actions menu and the views menu apart", () => {
		const actions = i18n.getFixedT("en", "experimentPage")("header.more");
		const views = i18n.getFixedT("en", "uf4a")("header.moreViews");
		expect(actions).toBe("More actions");
		expect(views).toBe("Other views");
	});
});