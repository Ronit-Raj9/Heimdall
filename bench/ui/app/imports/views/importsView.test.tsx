import "@/lib/i18n";

import type { ImportList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ImportsView from "./importsView";

const query = vi.hoisted(() => ({ result: {} as Record<string, unknown> }));

vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetImportsQuery: () => query.result,
}));

const fixture = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../public/fixtures/imports.json"), "utf8")) as ImportList;
const idle = { isLoading: false, isFetching: false, isError: false, data: undefined, error: undefined, refetch: () => undefined };

describe("ImportsView", () => {
	beforeEach(() => {
		query.result = { ...idle };
	});

	it("lists the importers with the untrusted-data note", () => {
		query.result = { ...idle, data: fixture };
		const html = renderToStaticMarkup(<ImportsView />);
		expect(html).toContain('data-testid="imports-list"');
		expect(html).toContain('data-testid="imports-untrusted-note"');
		expect(html).toContain('data-testid="imports-synthetic-banner"');
	});

	it("shows a failure as an error", () => {
		query.result = { ...idle, isError: true, error: { status: 503, data: { error: { message: "unavailable" } } } };
		const html = renderToStaticMarkup(<ImportsView />);
		expect(html).toContain('data-testid="imports-error"');
		expect(html).toContain("unavailable");
	});

	it("says when the server links no importers", () => {
		query.result = { ...idle, data: { mode: "live", importers: [] } };
		expect(renderToStaticMarkup(<ImportsView />)).toContain('data-testid="imports-empty"');
	});
});