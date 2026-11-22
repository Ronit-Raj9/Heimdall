import "@/lib/i18n";

import { routerState } from "@/lib/testing/routerMock";
import { render, tagOf } from "@/lib/testing/render";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NotFoundComponent } from "./__notFound";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

function at(pathname: string, search: Record<string, unknown> = {}) {
	routerState.location = { pathname, href: pathname, search };
}

afterEach(() => at("/verdict"));

describe("not-found page (N129, N73)", () => {
	it("redirects a label-shaped address to its page", () => {
		at("/live-sessions");
		expect(tagOf(render(<NotFoundComponent />), "navigate")).toContain('data-to="/live"');
		at("/labelling");
		expect(tagOf(render(<NotFoundComponent />), "navigate")).toContain('data-to="/annotation"');
		at("/drift", { x: "1" });
		expect(tagOf(render(<NotFoundComponent />), "navigate")).toContain('data-to="/scheduled-checks?tab=drift&amp;x=1"');
	});

	it("suggests sidebar pages for an unknown address that shares a word with one", () => {
		at("/live-stuff");
		const html = render(<NotFoundComponent />);
		expect(html).toContain("Page not found");
		expect(html).not.toContain('data-testid="navigate"');
		expect(tagOf(html, "not-found-suggest-live")).toContain('href="/live"');
		expect(html).toContain("Live sessions");
	});

	it("suggests nothing when no word matches", () => {
		at("/zz-qq");
		expect(render(<NotFoundComponent />)).not.toContain("not-found-suggestions");
	});
});