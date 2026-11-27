import { Inbox } from "lucide-react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { FeatureHero, hasTitle } from "./featureHero";

describe("FeatureHero title", () => {
	it("treats null, false and blank strings as no title", () => {
		expect(hasTitle(null)).toBe(false);
		expect(hasTitle(undefined)).toBe(false);
		expect(hasTitle(false)).toBe(false);
		expect(hasTitle("  ")).toBe(false);
		expect(hasTitle("Schedules")).toBe(true);
	});

	it("renders no heading element for an empty title", () => {
		const html = renderToStaticMarkup(<FeatureHero icon={Inbox} title="" message="Nothing yet." />);
		expect(html).not.toContain("<h2");
		expect(html).toContain("Nothing yet.");
	});

	it("renders the heading when a title is given", () => {
		const html = renderToStaticMarkup(<FeatureHero icon={Inbox} title="Schedules" message="Nothing yet." />);
		expect(html).toContain("<h2");
		expect(html).toContain("Schedules");
	});
});
