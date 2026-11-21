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
	it("renders the six groups with toggles and labelled lists, Advanced closed", () => {
		at("/");
		const html = render(<BenchSidebar />);
		for (const group of ["start", "benchmark", "build", "production", "govern", "operate"]) {
			expect(html).toContain(`data-testid="nav-group-${group}"`);
			const toggle = tagOf(html, `nav-group-toggle-${group}`) ?? "";
			expect(toggle).toContain(group === "operate" ? 'aria-expanded="false"' : 'aria-expanded="true"');
			expect(toggle).toContain(`aria-controls="nav-group-${group}"`);
		}
		expect(tagOf(html, "nav-group-toggle-operate")).toContain("Advanced");
		expect(html).toContain('aria-label="Heimdall navigation"');
		expect(html).not.toContain("nav-link-new-experiment");
		expect(html).not.toContain("nav-drawer");
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
		expect(html).toContain('id="drawer-group-benchmark"');
		expect(html).toContain('id="nav-group-benchmark"');
	});

	it("shows Settings only to a signed-in hosted user", () => {
		const has = (session: Parameters<typeof visibleNavGroups>[0]) =>
			visibleNavGroups(session).some((group) => group.items.some((item) => item.to === "/settings"));
		expect(has(LOCAL_SESSION)).toBe(false);
		expect(has(demoSession())).toBe(false);
		expect(has(hostedSession("viewer"))).toBe(true);
	});
});

describe("BenchHeader", () => {
	it("offers New experiment and the menu button in local mode", () => {
		at("/");
		const html = render(<BenchHeader onOpenNav={() => undefined} />);
		expect(tagOf(html, "header-new-experiment")).toContain('href="/experiments/new"');
		expect(tagOf(html, "nav-menu-button")).toContain('aria-label="Open navigation"');
		expect(tagOf(html, "nav-menu-button")).toContain('aria-expanded="false"');
		expect(html).toMatch(/>(Live|Sample) data</);
	});

	it("hides New experiment from viewers, demo visitors and the form itself", () => {
		at("/");
		expect(render(<BenchHeader />, hostedSession("viewer"))).not.toContain("header-new-experiment");
		expect(render(<BenchHeader />, demoSession())).not.toContain("header-new-experiment");
		expect(render(<BenchHeader />, hostedSession("member"))).toContain("header-new-experiment");
		at("/experiments/new");
		expect(render(<BenchHeader />)).not.toContain("header-new-experiment");
	});

	it("hides New experiment on an offline server that cannot launch (6-A-06)", () => {
		at("/");
		health.launch = false;
		expect(render(<BenchHeader />, LOCAL_SESSION)).not.toContain("header-new-experiment");
		health.launch = true;
		expect(render(<BenchHeader />, LOCAL_SESSION)).toContain("header-new-experiment");
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