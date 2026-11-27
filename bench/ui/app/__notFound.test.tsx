import "@/lib/i18n";

import { routerState } from "@/lib/testing/routerMock";
import { render, tagOf } from "@/lib/testing/render";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NotFoundComponent, suggestPages } from "./__notFound";

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
		expect(html).toContain("Live scoring");
	});

	it("suggests nothing when no word matches", () => {
		at("/zz-qq");
		expect(render(<NotFoundComponent />)).not.toContain("not-found-suggestions");
	});

	// M8-78, M8-79, X-65: always a way on, a plain description, a primary Home button.
	it("always offers four pages, a guide search and Home as the main button", () => {
		at("/zz-qq");
		const html = render(<NotFoundComponent />);
		for (const k of ["evaluations", "agents", "runs", "docs"]) expect(html).toContain(`data-testid="not-found-go-${k}"`);
		expect(html).toContain('data-testid="not-found-docs-search"');
		expect(html).toContain("We could not find that page.");
		expect(tagOf(html, "not-found-link-home")).toContain("bg-primary");
	});

	it("matches the start of a word", () => {
		expect(suggestPages("/eval").map((s) => s.to)).toContain("/evaluations");
	});
});
