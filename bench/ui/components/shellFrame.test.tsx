import "@/lib/i18n";

import { LOCAL_SESSION, demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, okQuery, render, tagOf } from "@/lib/testing/render";
import { routerState } from "@/lib/testing/routerMock";
import { afterEach, describe, expect, it, vi } from "vitest";
import { BenchHeader } from "./benchHeader";
import { BenchSidebar, visibleNavGroups } from "./benchSidebar";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
const health = vi.hoisted(() => ({ launch: undefined as boolean | undefined }));
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetHealthQuery: () => okQuery({ mode: "live", status: "ok", version: "test", launch: health.launch }),
}));
vi.mock("@/lib/store/apis/platformApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/platformApi")>()),
	useListJobsQuery: () => okQuery({ mode: "live", jobs: [], kinds: [] }),
	useSwitchOrgMutation: () => mutation(),
	useLogoutMutation: () => mutation(),
}));

function at(pathname: string) {
	routerState.location = { pathname, href: pathname, search: {} };
}

afterEach(() => {
	at("/verdict");
	health.launch = undefined;
});

describe("BenchSidebar", () => {
	it("renders Home on top, then four headed groups with toggles and labelled lists, all open, then the bottom group", () => {
		at("/");
		const html = render(<BenchSidebar />);
		expect(html).toContain('data-testid="nav-group-home"');
		expect(html).not.toContain("nav-group-toggle-home");
		expect(html).toContain('data-testid="nav-group-bottom"');
		expect(html).not.toContain("nav-group-toggle-bottom");
		for (const group of ["test", "results", "benchmarks", "production"]) {
			expect(html).toContain(`data-testid="nav-group-${group}"`);
			const toggle = tagOf(html, `nav-group-toggle-${group}`) ?? "";
			expect(toggle).toContain('aria-expanded="true"');
			expect(toggle).toContain(`aria-controls="nav-group-${group}"`);
		}
		expect(tagOf(html, "nav-group-toggle-test")).toContain("Test");
		expect(tagOf(html, "nav-link-experiments")).toContain('title="Comparisons:');
		expect(tagOf(html, "nav-link-failure-tests")).toContain('href="/failure-tests"');
		expect(tagOf(html, "nav-link-why-agents-fail")).toContain('href="/why-agents-fail"');
		expect(html).toContain('aria-label="Heimdall navigation"');
		expect(html).not.toContain("nav-link-new-experiment");
		expect(html).not.toContain("nav-link-welcome");
		expect(html).not.toContain("nav-drawer");
		// Spec 1: the unlinked pages have entries; only the first group toggle is a tab stop.
		for (const id of ["nav-link-inbox", "nav-link-gateway", "nav-link-docs"]) expect(html).toContain(id);
		expect(tagOf(html, "nav-group-toggle-test")).toContain('tabindex="0"');
		expect(tagOf(html, "nav-group-toggle-results")).toContain('tabindex="-1"');
	});

	it("shows Improvements to admins only", () => {
		const has = (session: Parameters<typeof visibleNavGroups>[0]) =>
			visibleNavGroups(session).some((group) => group.items.some((item) => item.to === "/improvements"));
		expect(has(LOCAL_SESSION)).toBe(true);
		expect(has(hostedSession("member"))).toBe(false);
		expect(has(hostedSession("admin"))).toBe(true);
	});

	it("highlights Comparisons on /compare opened from a comparison", () => {
		routerState.location = { pathname: "/compare", href: "/compare?exp=e_1", search: { exp: "e_1" } };
		const html = render(<BenchSidebar />);
		expect(tagOf(html, "nav-link-experiments")).toContain('aria-current="page"');
		expect(tagOf(html, "nav-link-runs")).not.toContain("aria-current");
	});

	it("marks the object's entry current on a nested route, and only that one", () => {
		at("/experiments/e_1");
		const html = render(<BenchSidebar />);
		expect(tagOf(html, "nav-link-experiments")).toContain('aria-current="page"');
		expect(tagOf(html, "nav-link-home")).not.toContain("aria-current");
		expect(html.match(/aria-current="page"/g)).toHaveLength(1);
		at("/agents/diff");
		expect(tagOf(render(<BenchSidebar />), "nav-link-agents")).toContain('aria-current="page"');
	});

	it("opens as a modal drawer with a close button below md", () => {
		at("/runs");
		const html = render(<BenchSidebar drawerOpen onDrawerClose={() => undefined} />);
		expect(html).toContain('data-testid="nav-drawer"');
		expect(html).toContain('role="dialog"');
		expect(html).toContain('aria-modal="true"');
		expect(tagOf(html, "nav-drawer-close")).toContain('aria-label="Close navigation"');
		// The drawer's lists have their own ids, so the page has no duplicate ids.
		expect(html).toContain('id="drawer-group-test"');
		expect(html).toContain('id="nav-group-test"');
	});

	it("shows the one Settings entry to every session, local and hosted", () => {
		const has = (session: Parameters<typeof visibleNavGroups>[0]) =>
			visibleNavGroups(session).some((group) => group.items.some((item) => item.to === "/settings"));
		expect(has(LOCAL_SESSION)).toBe(true);
		expect(has(hostedSession("viewer"))).toBe(true);
	});

	it("keeps an old route's page highlighted under its new owner", () => {
		at("/models");
		expect(tagOf(render(<BenchSidebar />), "nav-link-settings")).toContain('aria-current="page"');
		at("/canaries");
		expect(tagOf(render(<BenchSidebar />), "nav-link-scheduled-checks")).toContain('aria-current="page"');
	});
});

describe("BenchHeader", () => {
	it("offers Evaluate and the menu button in local mode", () => {
		at("/");
		const html = render(<BenchHeader onOpenNav={() => undefined} />);
		expect(tagOf(html, "header-evaluate")).toContain('href="/evaluate"');
		expect(html).toContain(">Evaluate an agent<");
		expect(tagOf(html, "nav-menu-button")).toContain('aria-label="Open navigation"');
		expect(tagOf(html, "nav-menu-button")).toContain('aria-expanded="false"');
		expect(html).toMatch(/>(Live|Sample) data</);
	});

	it("hides Evaluate from viewers, demo visitors and the wizard itself", () => {
		at("/");
		expect(render(<BenchHeader />, hostedSession("viewer"))).not.toContain("header-evaluate");
		expect(render(<BenchHeader />, demoSession())).not.toContain("header-evaluate");
		expect(render(<BenchHeader />, hostedSession("member"))).toContain("header-evaluate");
		at("/evaluate");
		expect(render(<BenchHeader />)).not.toContain("header-evaluate");
	});

	it("hides Evaluate on an offline server that cannot launch (6-A-06)", () => {
		at("/");
		health.launch = false;
		expect(render(<BenchHeader />, LOCAL_SESSION)).not.toContain("header-evaluate");
		health.launch = true;
		expect(render(<BenchHeader />, LOCAL_SESSION)).toContain("header-evaluate");
	});

	it("keeps the platform pieces in hosted mode", () => {
		at("/");
		const html = render(<BenchHeader />, hostedSession("admin"));
		expect(render(<BenchSidebar />, hostedSession("admin"))).toContain('data-testid="org-switcher"');
		expect(html).not.toContain('data-testid="org-switcher"');
		expect(html).toContain('data-testid="user-menu-trigger"');
		expect(html).toContain('data-testid="jobs-indicator"');
	});
});