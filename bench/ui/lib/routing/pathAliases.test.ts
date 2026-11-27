import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { BENCH_NAV } from "@/lib/constants/navigation";
import { describe, expect, it } from "vitest";
import { PATH_ALIASES, aliasFor, aliasRedirect, labelSlug } from "./pathAliases";

describe("path aliases (N129, N73)", () => {
	it("sends label-shaped addresses to their pages", () => {
		expect(aliasFor("/live-sessions")).toBe("/live");
		expect(aliasFor("/live-sessions/")).toBe("/live");
		expect(aliasFor("/labelling")).toBe("/annotation");
		expect(aliasFor("/labeling")).toBe("/annotation");
		expect(aliasFor("/drift")).toBe("/scheduled-checks?tab=drift");
		expect(aliasFor("/live")).toBeUndefined();
		// /failures is a real route now (M7-1); the old wording lands on its first tab.
		expect(aliasFor("/failures")).toBeUndefined();
		expect(aliasFor("/production-failures")).toBe("/failures?tab=production");
		expect(aliasFor("/no-such-page")).toBeUndefined();
		expect(aliasFor("/constructor")).toBeUndefined();
	});

	it("keeps the typed query string", () => {
		expect(aliasRedirect("/live-sessions")).toEqual({ to: "/live", search: {} });
		expect(aliasRedirect("/live-sessions", { x: "1" })).toEqual({ to: "/live", search: { x: "1" } });
		expect(aliasRedirect("/drift", { x: "1" })).toEqual({ to: "/scheduled-checks", search: { tab: "drift", x: "1" } });
		expect(aliasRedirect("/nope")).toBeUndefined();
	});

	it("covers the address of every sidebar label: it is the page's path or an alias of it", () => {
		for (const item of BENCH_NAV) {
			const slug = labelSlug(i18n.t(item.labelKey, { ns: "bench", lng: "en" }));
			if (slug === item.to) continue;
			expect(PATH_ALIASES[slug], `${slug} for ${item.to}`).toBeDefined();
			expect(PATH_ALIASES[slug]?.split("?")[0]).toBe(item.to);
		}
	});
});