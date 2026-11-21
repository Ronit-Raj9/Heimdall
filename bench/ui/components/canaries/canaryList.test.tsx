import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CANARY_LIST_FIXTURE } from "./canary.fixtures";
import { CanaryList } from "./canaryList";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const rows = (html: string) => [...html.matchAll(/data-testid="canary-row-([^"]+)"/g)].map((m) => m[1]);

describe("CanaryList", () => {
	it("lists drifted first with state badges, targets, baseline and spend against the cap", () => {
		const html = renderToStaticMarkup(<CanaryList canaries={CANARY_LIST_FIXTURE.canaries} onSelect={() => {}} />);
		expect(rows(html)).toEqual(["nightly", "smoke-toy"]);
		expect(html).toMatch(/data-testid="canary-state-nightly"[^>]*>Got worse</);
		expect(html).toMatch(/data-state="steady" data-testid="canary-state-smoke-toy"/);
		expect(html).toContain("bedrock/claude-sonnet-4-5");
		expect(html).toContain("e_canary_1");
		expect(html).toMatch(/data-testid="canary-spend-smoke-toy"[^>]*>no limit \(practice mode\)</);
		expect(html).toContain('data-testid="canary-smoke-smoke-toy"');
	});

	it("marks the selected canary", () => {
		const html = renderToStaticMarkup(<CanaryList canaries={CANARY_LIST_FIXTURE.canaries} selected="smoke-toy" onSelect={() => {}} />);
		expect(html).toMatch(/aria-selected="true" data-testid="canary-row-smoke-toy"/);
	});
});