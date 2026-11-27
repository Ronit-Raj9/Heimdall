import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it } from "vitest";
import { ActionCard, panelShown } from "./compareActionCard";

// Round 2 lane RC: a closed card asks for nothing (Q-20, V4-1) and a header-only card has no empty band on a phone (Q-177).
describe("ActionCard", () => {
	it("mounts a mountWhenOpen panel only once opened", () => {
		expect(panelShown(true, false)).toBe(false);
		expect(panelShown(true, true)).toBe(true);
		expect(panelShown(undefined, false)).toBe(true);
		const closed = render(
			<ActionCard title="T" description="D" mountWhenOpen>
				<span data-testid="inner" />
			</ActionCard>,
		);
		expect(closed).not.toContain('data-testid="inner"');
		const open = render(
			<ActionCard title="T" description="D" mountWhenOpen defaultOpen>
				<span data-testid="inner" />
			</ActionCard>,
		);
		expect(open).toContain('data-testid="inner"');
	});
	it("gives a header-only card a basis only in the row layout", () => {
		const html = render(<ActionCard title="T" description="D" action={<button type="button">Go</button>} />);
		expect(html).toContain("sm:basis-48");
		expect(html).not.toMatch(/[" ]basis-48/);
	});
});
describe("githubReposError (V4-1)", () => {
	it("shows the server's message of a 409", async () => {
		const { githubReposError } = await import("./compareRefsForm");
		expect(githubReposError({ status: 409, data: { error: { message: "GitHub needs reconnecting" } } })).toBe("GitHub needs reconnecting");
		expect(githubReposError({ status: 500 })).toBeNull();
	});
});