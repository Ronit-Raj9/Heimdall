import "@/lib/i18n";

import i18n from "@/lib/i18n";
import { resolveTab } from "@/components/shell/routeTabs";
import { clustersRedirect, inboxRedirect, triageRedirect, whyAgentsFailRedirect, type RouteSearch } from "@/lib/routing/u3Redirects";
import { describe, expect, it } from "vitest";
import { failuresTabs } from "./failuresView";
import "./ufd2I18n";

const tabs = failuresTabs((key) => i18n.t(`failures.tabs.${key}` as never, { ns: "ufd2" }));

// Every old route and tab id against the /failures tab that shows what the old tab showed
// (read from cc04f6abb): /inbox had inbox, clusters, triage and findings; /why-agents-fail had
// patterns (failure patterns per agent version, now "agents") and groups (the clusters view).
const OLD_LINKS: readonly [string, (s: RouteSearch) => { to: string; search: RouteSearch }, string | null, string][] = [
	["/inbox", inboxRedirect, null, "production"],
	["/inbox", inboxRedirect, "inbox", "production"],
	["/inbox", inboxRedirect, "clusters", "groups"],
	["/inbox", inboxRedirect, "triage", "patterns"],
	["/inbox", inboxRedirect, "findings", "findings"],
	["/inbox", inboxRedirect, "production", "production"],
	["/inbox", inboxRedirect, "groups", "groups"],
	["/inbox", inboxRedirect, "patterns", "patterns"],
	["/inbox", inboxRedirect, "agents", "agents"],
	["/why-agents-fail", whyAgentsFailRedirect, null, "agents"],
	["/why-agents-fail", whyAgentsFailRedirect, "patterns", "agents"],
	["/why-agents-fail", whyAgentsFailRedirect, "groups", "groups"],
	["/clusters", clustersRedirect, null, "groups"],
	["/triage", triageRedirect, null, "patterns"],
];

describe("old Failures links open the tab that shows what the old tab showed (Q-6)", () => {
	it.each(OLD_LINKS)("%s?tab=%s opens /failures?tab=%s with the other keys kept", (_route, redirect, tab, want) => {
		const search: RouteSearch = { agent: "a1", exp: "e_1", item: "inc-1", q: "x", ...(tab ? { tab } : {}) };
		const out = redirect(search);
		expect(out.to).toBe("/failures");
		expect(out.search).toEqual({ agent: "a1", exp: "e_1", item: "inc-1", q: "x", tab: want });
		// The page itself opens that tab, not a fallback.
		expect(resolveTab(tabs, String(out.search.tab), "production")).toBe(want);
	});

	// u3Redirects.ts belongs to lane R0; these turn into checks once its fallbacks land (notes/RH.md).
	it.todo("/inbox?tab=tracked opens findings (INBOX_TAB_MAP has no tracked)");
	it.todo("/why-agents-fail?tab=findings|production|tracked opens that tab, not agents");
});
