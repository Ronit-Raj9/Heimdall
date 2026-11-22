import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { VerdictPromote } from "./verdictTab";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("VerdictPromote (J7.promote)", () => {
	it("is a Promote this version button to the release form for two setups", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A", "B"]} />);
		const tag = tagOf(html, "verdict-promote") ?? "";
		expect(tag).toContain('href="/releases?exp=e1"');
		expect(tag).toMatch(/data-slot="button"|inline-flex/);
		expect(html).toContain("Promote this version");
	});
	it("opens the evaluation on a one-setup run of an evaluation", () => {
		const html = render(<VerdictPromote expId="e1" arms={["A"]} evaluationId="ev_9" />);
		expect(tagOf(html, "verdict-promote")).toContain('href="/evaluations/ev_9"');
		expect(html).toContain("Promote this version");
	});
	it("is absent on one setup outside an evaluation", () => {
		expect(render(<VerdictPromote expId="e1" arms={["A"]} />)).not.toContain("verdict-promote");
	});
});