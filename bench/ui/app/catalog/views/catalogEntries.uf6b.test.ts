import type { CatalogEntry } from "@/lib/types/catalog";
import { describe, expect, it } from "vitest";
import { displayFor } from "./catalogDisplay";
import { catalogEntries, isInternalEntry } from "./catalogModel";
import { addQuestion } from "./catalogView";

const e = (id: string, extra: Partial<CatalogEntry> = {}) =>
	({ id, title: id, kind: "bundled", summary: "", available: true, installed: false, needs_data: false, ...extra }) as CatalogEntry;

describe("catalog entries (N6-1, M6-64, M6-48)", () => {
	it("leaves out internal suites and test fixtures", () => {
		expect(isInternalEntry(e("suite-heimdall-improver", { suite_id: "heimdall-improver" }))).toBe(true);
		expect(isInternalEntry(e("suite-zz-test-x"))).toBe(true);
		expect(isInternalEntry(e("x", { internal: true } as Partial<CatalogEntry>))).toBe(true);
		expect(isInternalEntry(e("suite-coding-core", { suite_id: "coding-core" }))).toBe(false);
	});

	it("lists each suite once, the first entry winning", () => {
		const out = catalogEntries([
			e("terminal-bench-2", { suite_id: "hd-harbor-tb2" }),
			e("suite-hd-harbor-tb2", { suite_id: "hd-harbor-tb2" }),
			e("example-acme-memory", { kind: "example" }),
			e("suite-heimdall-improver", { suite_id: "heimdall-improver" }),
		]);
		expect(out.map((x) => x.id)).toEqual(["terminal-bench-2", "example-acme-memory"]);
	});

	it("drops importer text from descriptions", () => {
		const d = displayFor(e("suite-x", { suite_id: "x", summary: "Imported by bifrost-bench import harbor from y." }));
		expect(d.description).toBe("");
	});
});

describe("catalog Add asks first (M6-62)", () => {
	it("names the benchmark and its id", () => {
		const tu = (k: string, o: Record<string, string>) => `${k}:${JSON.stringify(o)}`;
		expect(addQuestion(e("terminal-bench-2", { suite_id: "hd-harbor-tb2" }), tu)).toBe(
			'catalog.addAsk:{"name":"Terminal-Bench 2.0","id":"hd-harbor-tb2"}',
		);
		expect(addQuestion(e("example-acme-memory", { kind: "example" }), tu)).toContain("catalog.addAskExample");
	});
});
