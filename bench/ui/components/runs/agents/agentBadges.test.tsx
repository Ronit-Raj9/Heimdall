import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AgentId, ExitBadge, RoleChip, StrengthBadge, UnknownValue } from "./agentBadges";

describe("agent badges", () => {
	it("renders unknown as hatched text with a hint, never zero", () => {
		const html = renderToStaticMarkup(<UnknownValue testId="u" />);
		expect(html).toContain('data-state="unknown"');
		expect(html).toContain(">unknown<");
		expect(html).toContain("bench-hatched");
		expect(html).toContain("gateway rows that carry the agent&#x27;s id");
	});

	it("labels strengths with their source, dashing all but proven", () => {
		const proven = renderToStaticMarkup(<StrengthBadge strength="proven" source="gateway_key" />);
		expect(proven).toContain('data-tone="good"');
		expect(proven).not.toContain("border-dashed");
		expect(proven).toContain("via gateway key");
		const odd = renderToStaticMarkup(<StrengthBadge strength={"<x>" as never} source="<src>" />);
		expect(odd).toContain("&lt;x&gt;");
		expect(odd).toContain("via &lt;src&gt;");
		expect(odd).toContain("border-dashed");
	});

	it("translates known exits and shows others as text", () => {
		expect(renderToStaticMarkup(<ExitBadge exit="completed" />)).toContain(">completed<");
		expect(renderToStaticMarkup(<ExitBadge exit="exit code 2" />)).toContain('data-tone="bad"');
		expect(renderToStaticMarkup(<ExitBadge exit={undefined} />)).toBe("");
	});

	it("clips ids and roles and says when there is no agent", () => {
		const long = "a".repeat(100);
		const html = renderToStaticMarkup(<AgentId id={long} />);
		expect(html).toContain("…");
		expect(renderToStaticMarkup(<AgentId id="" />)).toContain("no agent");
		expect(renderToStaticMarkup(<RoleChip role={undefined} />)).toBe("");
	});
});