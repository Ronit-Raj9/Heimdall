import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { RootErrorComponent, RouteErrorComponent } from "./__error";

vi.mock("@tanstack/react-router", async () => ({
	...(await import("@/lib/testing/routerMock")).routerMock,
	useRouter: () => ({ invalidate: () => Promise.resolve() }),
}));

describe("route error", () => {
	it("says the page failed, shows the error text and offers a retry and the way home", () => {
		const html = render(<RouteErrorComponent error={new TypeError("Cannot read properties of null (reading '0')")} />);
		expect(tagOf(html, "route-error")).toContain('role="alert"');
		expect(html).toContain("This page failed to load");
		expect(html).toContain("Cannot read properties of null (reading &#x27;0&#x27;)");
		expect(html).toContain('data-testid="route-error-retry"');
		expect(tagOf(html, "route-error-home")).toContain('href="/"');
	});
	it("renders on its own when the shell itself failed", () => {
		expect(render(<RootErrorComponent error={new Error("boom")} />)).toContain("boom");
	});
});