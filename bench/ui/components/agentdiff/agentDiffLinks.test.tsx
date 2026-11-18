import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CompareVersionsLink } from "./agentDiffLinks";

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, search, children, ...rest }: { to: string; search?: Record<string, string>; children: React.ReactNode }) => {
		const qs = new URLSearchParams(search ?? {}).toString();
		return (
			<a href={qs ? `${to}?${qs}` : to} {...rest}>
				{children}
			</a>
		);
	},
}));

describe("CompareVersionsLink", () => {
	it("opens the Diff tab of the agents page with no selection", () => {
		const html = renderToStaticMarkup(<CompareVersionsLink />);
		expect(html).toContain('href="/agents?tab=diff"');
		expect(html).toContain('data-testid="agentdiff-compare-link"');
		expect(html).toContain("Compare versions");
	});

	it("names the agent when one is given", () => {
		const html = renderToStaticMarkup(<CompareVersionsLink agent="codex" />);
		expect(html).toContain('href="/agents?tab=diff&amp;agent=codex"');
		expect(html).toContain('data-testid="agentdiff-compare-link-codex"');
	});
});