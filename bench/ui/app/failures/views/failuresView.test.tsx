import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { resolveTab } from "@/components/shell/routeTabs";
import { describe, expect, it } from "vitest";
import { failuresTabs } from "./failuresView";
import "./ufd2I18n";

const tabs = failuresTabs((key) => i18n.t(`failures.tabs.${key}` as never, { ns: "ufd2" }));

describe("Failures page tabs (M7-1)", () => {
	it("lists the tabs in order with their names", () => {
		expect(tabs.map((tab) => tab.id)).toEqual(["production", "findings", "groups", "patterns", "agents"]);
		expect(tabs.map((tab) => tab.label)).toEqual(["Production failures", "Findings", "Failure groups", "Failure patterns", "By agent version"]);
	});

	it("opens the old tab ids on their new tab and defaults to production", () => {
		expect(resolveTab(tabs, null, "production")).toBe("production");
		expect(resolveTab(tabs, "inbox", "production")).toBe("production");
		expect(resolveTab(tabs, "clusters", "production")).toBe("groups");
		expect(resolveTab(tabs, "triage", "production")).toBe("patterns");
		expect(resolveTab(tabs, "connect", "production")).toBe("production");
	});
});
