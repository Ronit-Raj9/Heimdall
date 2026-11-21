import "@/lib/i18n";

import type { CatalogEntry } from "@/lib/types/catalog";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { NO_FILTERS } from "./catalogModel";
import { CatalogBody, type CatalogBodyProps, cleanTitle, entryCategories, filterEntries, importReady, initialImportForm, sectionOf } from "./catalogView";

const noop = () => {};
const entries: CatalogEntry[] = [
	{ id: "terminal-bench-2", title: "Terminal-Bench 2.0", kind: "importer", summary: "terminal tasks", importer: "harbor", suite_id: "hd-harbor-tb2", available: true, installed: false, needs_data: true, tags: ["terminal"],
		upload_expects: "zip of Harbor task folders", fetch_allowed: true, fetch_url: "https://github.com/x/y/archive/main.zip", dataset_path: "uploads/datasets/terminal-bench-2" },
	{ id: "example-acme-memory", title: "Acme memory example: does an agent remember across sessions?", kind: "example", summary: "memory", suites: ["acme-memory"], agents: ["noop"], available: true, installed: false, needs_data: false, tags: ["example"] },
	{ id: "suite-coding-core", title: "coding-core", kind: "bundled", summary: "starter", suite_id: "coding-core", available: true, installed: true, needs_data: false },
	{ id: "import-gpu", title: "GPU thing", kind: "importer", summary: "x", available: false, installed: false, needs_data: true, track: "ml_engineering" },
];
const base: CatalogBodyProps = { entries, sourceAvailable: true, starterAgents: ["codex", "noop"], filters: NO_FILTERS, onFilters: noop, onAdd: noop };

describe("CatalogBody", () => {
	it("lists entries with add, installed and unavailable states", () => {
		const html = renderToStaticMarkup(<CatalogBody {...base} />);
		expect(html).toContain('data-testid="catalog-add-terminal-bench-2"');
		expect(html).toContain('data-testid="catalog-add-example-acme-memory"');
		expect(html).toContain('data-testid="catalog-installed-suite-coding-core"');
		expect(html).toContain('data-testid="catalog-open-suite-coding-core"');
		expect(html).toContain('data-testid="catalog-unavailable-import-gpu"');
		expect(html).not.toContain('data-testid="catalog-add-import-gpu"');
		expect(html).toContain("codex, noop");
		expect(html).not.toContain('data-testid="catalog-no-source"');
	});

	it("cleans titles, pills and groups sections", () => {
		const html = renderToStaticMarkup(<CatalogBody {...base} />);
		expect(cleanTitle(entries[1])).toEqual({ name: "Memory", tagline: "Does an agent remember across sessions?", example: true });
		expect(cleanTitle(entries[2]).name).toBe("Coding core");
		expect(html).toContain(">Memory<");
		expect(html).toContain("Free");
		expect(html).toContain('data-testid="catalog-section-featured"');
		expect(html).toContain('data-testid="catalog-section-try"');
		expect(html).toContain('data-testid="catalog-section-import"');
		expect(sectionOf(entries[0])).toBe("featured");
		expect(sectionOf(entries[1])).toBe("try");
		expect(sectionOf(entries[3])).toBe("import");
		expect(html).toContain('data-testid="catalog-filter-gpu"');
		expect(html).not.toContain('data-testid="catalog-data-terminal-bench-2"');
	});

	it("filters by category, source and query", () => {
		expect(entryCategories(entries[0])[0]).toBe("terminal");
		expect(entryCategories(entries[1])[0]).toBe("memory");
		expect(entryCategories(entries[3])[0]).toBe("gpu");
		expect(filterEntries(entries, { ...NO_FILTERS, category: "memory" }).map((e) => e.id)).toEqual(["example-acme-memory"]);
		expect(filterEntries(entries, { ...NO_FILTERS, source: "ready" }).map((e) => e.id)).toEqual(["example-acme-memory", "suite-coding-core"]);
		expect(filterEntries(entries, { ...NO_FILTERS, source: "needs" }).map((e) => e.id)).toEqual(["terminal-bench-2", "import-gpu"]);
		expect(filterEntries(entries, { ...NO_FILTERS, query: "terminal" }).map((e) => e.id)).toEqual(["terminal-bench-2"]);
		expect(renderToStaticMarkup(<CatalogBody {...base} filters={{ ...NO_FILTERS, query: "zzz" }} />)).toContain('data-testid="catalog-empty"');
	});

	it("shows the import panel and the source warning", () => {
		const html = renderToStaticMarkup(
			<CatalogBody {...base} sourceAvailable={false} importFor={entries[0]} importForm={initialImportForm(entries[0])} />,
		);
		expect(html).toContain('data-testid="catalog-import-form"');
		expect(html).toContain('data-testid="catalog-no-source"');
		expect(html).toMatch(/data-testid="catalog-import-start"[^>]*disabled|disabled[^>]*data-testid="catalog-import-start"/);
		expect(html).toContain('data-testid="catalog-import-file"');
		expect(html).toContain("zip of Harbor task folders");
		expect(html).toContain('data-testid="catalog-mode-url"');
	});

	it("states the data options in the panel and prefills the URL", () => {
		const details = renderToStaticMarkup(<CatalogBody {...base} detailsFor={entries[0]} />);
		expect(details).toContain('data-testid="catalog-details-panel"');
		expect(details).toContain('data-testid="catalog-data-terminal-bench-2"');
		expect(details).toContain("Upload:");
		expect(details).toContain("hd-harbor-tb2");
		const f = initialImportForm(entries[0]);
		expect(f.url).toBe("https://github.com/x/y/archive/main.zip");
		expect(importReady(f)).toBe(false);
		expect(importReady({ ...f, mode: "url" })).toBe(true);
		expect(importReady({ ...f, mode: "url", url: "http://10.0.0.1/x" })).toBe(false);
		const urlForm = renderToStaticMarkup(<CatalogBody {...base} importFor={entries[0]} importForm={{ ...f, mode: "url" }} />);
		expect(urlForm).toContain('value="https://github.com/x/y/archive/main.zip"');
		const noFetch = renderToStaticMarkup(<CatalogBody {...base} importFor={{ ...entries[0], fetch_allowed: false }} importForm={f} />);
		expect(noFetch).not.toContain('data-testid="catalog-mode-url"');
	});
});
