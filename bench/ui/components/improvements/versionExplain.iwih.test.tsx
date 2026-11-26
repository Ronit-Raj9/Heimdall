import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { EXPLAIN_FIXTURE } from "./improve.fixtures";
import { VersionExplainView, agentOfRef } from "./versionExplain";

const links: { to?: string; params?: unknown; search?: unknown }[] = [];
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, ...p }: { children: React.ReactNode; to?: string; params?: unknown; search?: unknown }) => {
		links.push(p);
		return <a data-testid="link">{children}</a>;
	},
}));

describe("VersionExplainView link to the agent's versions", () => {
	it("links a kept version to its agent's saved versions", () => {
		links.length = 0;
		const html = render(<VersionExplainView explain={EXPLAIN_FIXTURE} />);
		expect(html).toContain("Open the agent&#x27;s saved versions");
		expect(links).toEqual([expect.objectContaining({ to: "/agents/$agentId", params: { agentId: "coder" }, search: { tab: "versions" } })]);
	});

	it("shows no link for a ref that is not a saved agent version", () => {
		links.length = 0;
		render(<VersionExplainView explain={{ ...EXPLAIN_FIXTURE, ref: "improvements/x.yaml" }} />);
		expect(links).toEqual([]);
		expect(agentOfRef("agents/a-b.c_d.yaml@v12")).toBe("a-b.c_d");
		expect(agentOfRef("agents/../x.yaml@v1")).toBe("");
	});
});