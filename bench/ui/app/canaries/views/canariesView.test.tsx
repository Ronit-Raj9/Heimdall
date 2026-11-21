import "@/lib/i18n";

import { CANARY_LIST_FIXTURE } from "@/components/canaries/canary.fixtures";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CanariesBody } from "./canariesView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("CanariesBody", () => {
	it("lists the canaries, names a broken definition and shows the open panel", () => {
		const html = renderToStaticMarkup(
			<CanariesBody data={CANARY_LIST_FIXTURE} selected="nightly" onSelect={() => {}} panel={<div data-testid="panel" />} />,
		);
		expect(html).toContain('data-testid="canary-table"');
		expect(html).toContain('data-testid="canaries-errors"');
		expect(html).toContain("broken:");
		expect(html).toContain('data-testid="panel"');
	});

	it("explains how to start when there are no canaries", () => {
		const html = renderToStaticMarkup(
			<CanariesBody data={{ ...CANARY_LIST_FIXTURE, canaries: [], errors: [] }} selected={null} onSelect={() => {}} panel={null} />,
		);
		expect(html).toContain('data-testid="canaries-empty"');
		expect(html).toContain("canary init");
		expect(html).toContain("Catch your agent or model quietly getting worse");
	});
});