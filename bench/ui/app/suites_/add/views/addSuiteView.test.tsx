import "@/lib/i18n";

import type { ImportList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AddSuiteView from "./addSuiteView";

const query = vi.hoisted(() => ({ result: {} as Record<string, unknown> }));

vi.mock("@tanstack/react-router", () => ({
	Link: ({ to, children, ...rest }: { to: string; children: React.ReactNode }) => (
		<a href={to} {...rest}>
			{children}
		</a>
	),
}));

vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetImportsQuery: () => query.result,
}));

const fixture = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/imports.json"), "utf8")) as ImportList;
const idle = { isLoading: false, isFetching: false, isError: false, data: undefined, error: undefined, refetch: () => undefined };

describe("AddSuiteView", () => {
	beforeEach(() => {
		query.result = { ...idle };
	});

	it("lists the importers with the untrusted-data note", () => {
		query.result = { ...idle, data: fixture };
		const html = renderToStaticMarkup(<AddSuiteView />);
		expect(html).toContain('data-testid="imports-list"');
		expect(html).toContain('data-testid="imports-untrusted-note"');
		expect(html).toContain('data-testid="imports-synthetic-banner"');
	});

	it("offers bring your own beside the importers, as the Import tab of Benchmarks (no breadcrumb)", () => {
		query.result = { ...idle, data: fixture };
		const html = renderToStaticMarkup(<AddSuiteView />);
		expect(html).toContain('data-testid="suites-add-your-own"');
		expect(html).not.toContain("<nav aria-label=\"Breadcrumb\"");
	});

	it("shows a failure as an error", () => {
		query.result = { ...idle, isError: true, error: { status: 503, data: { error: { message: "unavailable" } } } };
		const html = renderToStaticMarkup(<AddSuiteView />);
		expect(html).toContain('data-testid="imports-error"');
		expect(html).toContain("unavailable");
	});

	it("says when the server links no importers", () => {
		query.result = { ...idle, data: { mode: "live", importers: [] } };
		expect(renderToStaticMarkup(<AddSuiteView />)).toContain('data-testid="imports-empty"');
	});
});