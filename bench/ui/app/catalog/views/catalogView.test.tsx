import "@/lib/i18n";

import type { CatalogEntry } from "@/lib/types/catalog";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { CatalogBody, type CatalogBodyProps, filterEntries, importReady, initialImportForm } from "./catalogView";

const noop = () => {};
const entries: CatalogEntry[] = [
	{ id: "terminal-bench-2", title: "Terminal-Bench 2.0", kind: "importer", summary: "terminal tasks", importer: "harbor", suite_id: "hd-harbor-tb2", available: true, installed: false, needs_data: true, tags: ["terminal"],
		upload_expects: "zip of Harbor task folders", fetch_allowed: true, fetch_url: "https://github.com/x/y/archive/main.zip", dataset_path: "uploads/datasets/terminal-bench-2" },
	{ id: "example-acme-memory", title: "Acme memory example", kind: "example", summary: "memory", suites: ["acme-memory"], agents: ["noop"], available: true, installed: false, needs_data: false },
	{ id: "suite-coding-core", title: "coding-core", kind: "bundled", summary: "starter", suite_id: "coding-core", available: true, installed: true, needs_data: false },
	{ id: "import-gpu", title: "GPU thing", kind: "importer", summary: "x", available: false, installed: false, needs_data: true },
];
const base: CatalogBodyProps = { entries, sourceAvailable: true, starterAgents: ["codex", "noop"], filter: "all", onFilter: noop, query: "", onQuery: noop, onAdd: noop };

describe("CatalogBody", () => {
	it("lists entries with add, installed and unavailable states", () => {
		const html = renderToStaticMarkup(<CatalogBody {...base} />);
		expect(html).toContain('data-testid="catalog-add-terminal-bench-2"');
		expect(html).toContain('data-testid="catalog-add-example-acme-memory"');
		expect(html).toContain('data-testid="catalog-installed-suite-coding-core"');
		expect(html).not.toContain('data-testid="catalog-add-import-gpu"');
		expect(html).toContain("codex, noop");
		expect(html).not.toContain('data-testid="catalog-no-source"');
	});

	it("filters by kind and query", () => {
		expect(filterEntries(entries, "example", "").map((e) => e.id)).toEqual(["example-acme-memory"]);
		expect(filterEntries(entries, "all", "terminal").map((e) => e.id)).toEqual(["terminal-bench-2"]);
		expect(renderToStaticMarkup(<CatalogBody {...base} query="zzz" />)).toContain('data-testid="catalog-empty"');
	});

	it("shows the import form and the source warning", () => {
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

	it("states the data options on importer cards and prefills the URL", () => {
		const html = renderToStaticMarkup(<CatalogBody {...base} />);
		expect(html).toContain('data-testid="catalog-data-terminal-bench-2"');
		expect(html).toContain("Upload:");
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
