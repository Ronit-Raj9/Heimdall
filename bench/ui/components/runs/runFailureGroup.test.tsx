import "@/lib/i18n";

import type { Run } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { sameFailureLink } from "./runFailureGroup";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, search, ...rest }: { children: unknown; search?: Record<string, string> }) => (
		<a data-search={JSON.stringify(search)} {...(rest as object)}>
			{children as never}
		</a>
	),
}));

const { RunFailureCard } = await import("./runFailureCard");

describe("same failure link (M5-1, G1)", () => {
	it("links only when the group holds other runs", () => {
		expect(sameFailureLink(undefined)).toBeUndefined();
		expect(sameFailureLink({ signature: "ab", count: 1 })).toBeUndefined();
		expect(sameFailureLink({ count: 4 })).toBeUndefined();
		expect(sameFailureLink({ signature: "ab", count: 3, exp_id: "e_1" })).toEqual({
			count: 3,
			search: { tab: "clusters", sig: "ab", exp: "e_1" },
		});
		expect(sameFailureLink({ signature: "ab", count: 2 }, "e_2")?.search.exp).toBe("e_2");
	});

	it("shows the count under the card, and nothing without a group", () => {
		const run = { outcome: "fail", failures: [] } as unknown as Run;
		const withGroup = renderToStaticMarkup(<RunFailureCard run={run} group={{ signature: "ab", count: 3, exp_id: "e_1" }} />);
		expect(withGroup).toContain("The same failure in 3 runs of this evaluation");
		expect(withGroup).toContain("&quot;sig&quot;:&quot;ab&quot;");
		const without = renderToStaticMarkup(<RunFailureCard run={run} />);
		expect(without).not.toContain("same failure");
	});
});

describe("failure group comes with the run answer (M5-1, C0 fold)", () => {
	it("has no private request of its own any more", async () => {
		const mod = (await import("./runFailureGroup")) as Record<string, unknown>;
		expect(mod.useGetRunFailureQuery).toBeUndefined();
		expect(mod.runFailureApi).toBeUndefined();
	});
});
