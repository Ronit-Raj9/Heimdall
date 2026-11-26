import "@/lib/i18n";

import type { CatalogEntry } from "@/lib/types/catalog";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

vi.mock("nuqs", async (importOriginal) => ({
	...(await importOriginal<typeof import("nuqs")>()),
	useQueryState: (key: string) => [key === "q" ? "bfcl" : null, () => undefined],
}));
const entries: CatalogEntry[] = [
	{ id: "bfcl", title: "BFCL", kind: "importer", summary: "function calls", available: true, installed: false, needs_data: true },
	{ id: "other", title: "Other", kind: "importer", summary: "something else", available: true, installed: false, needs_data: true },
];
vi.mock("@/lib/store/apis/catalogApi", () => ({
	useGetBenchCatalogQuery: () => ({
		data: { entries, source_available: true },
		isLoading: false,
		isError: false,
		refetch: () => undefined,
	}),
	useAddCatalogEntryMutation: () => [() => undefined],
	useUploadCatalogDatasetMutation: () => [() => undefined],
}));
vi.mock("@/lib/store", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store")>()),
	useGetSuitesQuery: () => ({ data: { suites: [] } }),
}));
vi.mock("@/lib/session/sessionContext", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/session/sessionContext")>()),
	useSession: () => ({ mode: "local", role: "owner" }),
	canUse: () => true,
}));

const { default: CatalogView, CatalogBody } = await import("./catalogView");
const { NO_FILTERS } = await import("./catalogModel");

describe("catalog wave 4 minors", () => {
	it("applies the search term from the address (p12-2)", () => {
		const html = renderToStaticMarkup(<CatalogView />);
		expect(html).toContain('value="bfcl"');
		expect(html).toContain('data-testid="catalog-add-bfcl"');
		expect(html).not.toContain('data-testid="catalog-add-other"');
	});

	it("gives the details drawer a focusable dialog panel (p09-1)", () => {
		const html = renderToStaticMarkup(
			<CatalogBody entries={entries} sourceAvailable filters={NO_FILTERS} onFilters={() => {}} onAdd={() => {}} detailsFor={entries[0]} />,
		);
		expect(html).toContain('role="dialog"');
		expect(html).toContain('tabindex="-1"');
	});
});