import "@/lib/i18n";

import { IMPROVEMENT_FIXTURE } from "@/components/improvements/improve.fixtures";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ImprovementsTable } from "./improvementsView";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("ImprovementsTable", () => {
	it("lists runs with status, rounds and held-out gain", () => {
		const html = renderToStaticMarkup(<ImprovementsTable rows={[IMPROVEMENT_FIXTURE]} />);
		expect(html).toContain('data-testid="improvements-table"');
		expect(html).toContain("notes-loop");
		expect(html).toContain("2 of 2");
		expect(html).toContain("+20.0 pts");
	});

	it("explains how to start when there are none", () => {
		const html = renderToStaticMarkup(<ImprovementsTable rows={[]} />);
		expect(html).toContain('data-testid="improvements-empty"');
		expect(html).toContain("improve create");
	});
});
