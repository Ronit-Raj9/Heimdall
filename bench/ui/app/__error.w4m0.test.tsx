import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { RouteErrorComponent, retryRoute } from "./__error";

vi.mock("@tanstack/react-router", async () => ({
	...(await import("@/lib/testing/routerMock")).routerMock,
	useRouter: () => ({ invalidate: () => Promise.resolve() }),
}));

// Wave 4, p16-5: the raw error sits behind a disclosure and a retry clears the cached data first.
describe("route error, wave 4", () => {
	it("shows the raw message only inside the technical details", () => {
		const html = render(<RouteErrorComponent error={new TypeError("e.filter is not a function")} />);
		const details = html.indexOf("<details");
		expect(details).toBeGreaterThan(-1);
		expect(html).toContain("Technical details");
		expect(html.indexOf("e.filter is not a function")).toBeGreaterThan(details);
		expect(html.slice(0, details)).not.toContain("e.filter");
	});
	it("resets the request cache before it retries", () => {
		const calls: string[] = [];
		retryRoute(
			(action) => {
				calls.push(`dispatch:${(action as { type: string }).type}`);
				return action;
			},
			() => calls.push("reset"),
			() => {
				calls.push("invalidate");
				return Promise.resolve();
			},
		);
		expect(calls[0]).toMatch(/^dispatch:.*resetApiState$/);
		expect(calls.slice(1)).toEqual(["reset", "invalidate"]);
	});
});