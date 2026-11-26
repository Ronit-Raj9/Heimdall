import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { describe, expect, it } from "vitest";
import { BENCH_NAV_GROUPS, activeNavItem } from "./navigation";

// Wave 4, N77: Safety monitors and Get started have their own sidebar items.

describe("navigation items of wave 4", () => {
	it("monitors and quickstart have their own item", () => {
		const home = BENCH_NAV_GROUPS.find((g) => g.id === "home")?.items ?? [];
		const watch = BENCH_NAV_GROUPS.find((g) => g.id === "watch")?.items ?? [];
		expect(home.at(-1)?.to).toBe("/quickstart");
		expect(home.at(-1)?.testId).toBe("nav-link-quickstart");
		expect(home[0].also ?? []).not.toContain("/quickstart");
		expect(watch.at(-1)?.to).toBe("/monitors");
		expect(watch.at(-1)?.testId).toBe("nav-link-monitors");
		expect(activeNavItem("/monitors")?.to).toBe("/monitors");
		expect(activeNavItem("/quickstart")?.to).toBe("/quickstart");
		expect(i18n.t("shell.nav.monitors", { ns: "bench" })).toBe("Safety monitors");
		expect(i18n.t("shell.nav.quickstart", { ns: "bench" })).toBe("Get started");
		expect(i18n.t("shell.navHint.monitors", { ns: "bench" })).toBe("Rules that watch runs and flag problems");
		expect(i18n.t("shell.navHint.quickstart", { ns: "bench" })).toBe("Set up Heimdall step by step");
	});
});