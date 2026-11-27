import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { BENCH_NAV_GROUPS, activeNavItem } from "./navigation";

// Wave 4, N77: Safety monitors has its own sidebar item. Fix wave S1 (Spec 1): it sits in the
// Production group, and the CLI quickstart moved from the sidebar into the help menu.

describe("navigation items of wave 4", () => {
	it("monitors has its own item; quickstart is not in the sidebar", () => {
		const home = BENCH_NAV_GROUPS.find((g) => g.id === "home")?.items ?? [];
		const production = BENCH_NAV_GROUPS.find((g) => g.id === "production")?.items ?? [];
		expect(home.map((item) => item.to)).toEqual(["/"]);
		expect(production.at(-1)?.to).toBe("/monitors");
		expect(production.at(-1)?.testId).toBe("nav-link-monitors");
		expect(activeNavItem("/monitors")?.to).toBe("/monitors");
		expect(activeNavItem("/quickstart")).toBeNull();
		expect(i18n.t("shell.nav.monitors", { ns: "bench" })).toBe("Safety monitor tests");
		expect(i18n.t("shell.navHint.monitors", { ns: "bench" })).toBe("Check that your safety monitors catch what they should.");
	});
});
