import { describe, expect, it } from "vitest";
import { SETTINGS_GROUPS, SETTINGS_TABS, settingsGroupsFor } from "./settingsView";

describe("settings rail groups", () => {
	it("places every tab in exactly one group", () => {
		const all = SETTINGS_GROUPS.flatMap((g) => g.tabs);
		expect([...all].sort()).toEqual([...SETTINGS_TABS].sort());
	});

	it("puts the credential kinds together, right after models and spend", () => {
		expect(SETTINGS_GROUPS[0]).toEqual({ id: "models", tabs: ["keys", "usage", "billing"] });
		expect(SETTINGS_GROUPS[1].tabs).toEqual(["secrets", "tokens", "github"]);
	});

	it("drops a group whose tabs are all hidden", () => {
		const groups = settingsGroupsFor(["keys", "runners", "usage", "files", "secrets"]);
		expect(groups.map((g) => g.id)).toEqual(["models", "credentials", "running", "project"]);
		expect(groups[1].tabs).toEqual(["secrets"]);
	});
});