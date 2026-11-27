import "@/lib/i18n";

import type { ImportList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ImporterCard, ImporterList } from "./importerCard";

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/imports.json"), "utf8")) as ImportList;
const importer = (name: string) => list.importers.find((i) => i.name === name)!;
const card = (name: string) => renderToStaticMarkup(<ImporterCard importer={importer(name)} />);

describe("ImporterCard", () => {
	it("shows benchmarks, track, grading, license, how to fetch and what a run needs", () => {
		const html = card("harbor");
		expect(html).toMatch(/data-testid="imports-card-harbor-benchmarks".*?terminal-bench@2.x/);
		expect(html).toMatch(/data-testid="imports-card-harbor-track"[^>]*>Terminal/);
		expect(html).toMatch(/data-testid="imports-card-harbor-grading" data-tone="good"/);
		expect(html).toMatch(/data-testid="imports-card-harbor-license".*?varies per task, see provenance/);
		expect(html).toMatch(/data-testid="imports-card-harbor-fetch".*?git clone --depth 1/);
		expect(html).toMatch(/data-testid="imports-card-harbor-needs".*?<li>a per-task image built from each task&#x27;s Dockerfile<\/li>/);
		expect(html).not.toContain("<a ");
		expect(html).not.toContain("imports-card-harbor-judged");
	});

	it("gives the exact import, lock and admit commands", () => {
		expect(card("tau2")).toContain(
			"heimdall import tau2 -from datasets/tau2 -suite tau2\nheimdall suite lock tau2\nheimdall suite admit tau2",
		);
	});

	it("explains a judged importer", () => {
		const html = card("longmemeval");
		expect(html).toMatch(/data-testid="imports-card-longmemeval-grading" data-tone="warn"/);
		expect(html).toContain('data-testid="imports-card-longmemeval-judged"');
		expect(html).toContain(">Judged<");
	});

	it("says when a run needs nothing extra and when no license is stated", () => {
		const html = renderToStaticMarkup(<ImporterCard importer={{ ...importer("tau2"), needs: [], license: "" }} />);
		expect(html).toContain("Nothing beyond the default stack.");
		expect(html).toContain("No license recorded");
	});
});

describe("ImporterList", () => {
	it("lists every importer in a labelled list", () => {
		const html = renderToStaticMarkup(<ImporterList importers={list.importers} />);
		expect(html).toContain('aria-label="Importers"');
		expect(html.match(/data-testid="imports-card-[a-z0-9]+"/g)).toHaveLength(list.importers.length);
	});

	it("says when the server links none", () => {
		expect(renderToStaticMarkup(<ImporterList importers={[]} />)).toContain("This server links no importers.");
	});
});