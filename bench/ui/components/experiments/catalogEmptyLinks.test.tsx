import "@/lib/i18n";

import ExperimentListView from "@/app/experiments/views/experimentListView";
import { AddBenchmarkMenu } from "@/app/suites/views/suitesView";
import { demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { CatalogEmptyLinks } from "./catalogEmptyLinks";
import { ADD_ENTRIES } from "@/components/suites/addChooser";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
// The list page asks how many results changed (M4-97).
vi.mock("@/lib/store/apis/validityApi", () => ({ useGetValidityQuery: () => ({ data: { entries: [], unseen: 0, stale: [] } }) }));
vi.mock("@/app/experiments/views/experimentBoard", () => ({ ExperimentBoard: () => <div data-testid="experiment-list" /> }));

describe("create links", () => {
	it("benchmarks list has one Add benchmark menu with the six ways to add tasks (M6-41)", () => {
		// The menu is closed in a static render; its entries are the shared ADD_ENTRIES.
		const html = render(<AddBenchmarkMenu />);
		expect(html).toContain("Add benchmark");
		expect(html.match(/data-testid="suites-add-menu"/g)).toHaveLength(1);
		expect(ADD_ENTRIES.map((e) => e.id)).toEqual(["file", "catalog", "api", "create", "code", "failures"]);
		expect(ADD_ENTRIES.find((e) => e.id === "create")?.to).toBe("/suites/new");
	});

	it("experiments list has New comparison for members only", () => {
		const html = render(<ExperimentListView />, hostedSession("member"));
		expect(html).toContain('data-testid="experiments-new"');
		expect(html).toContain('href="/experiments/new"');
		expect(render(<ExperimentListView />, hostedSession("viewer"))).not.toContain("experiments-new");
		expect(render(<ExperimentListView />, demoSession())).not.toContain("experiments-new");
	});

	it("an empty catalog offers the catalog and New suite when suites are missing", () => {
		const html = render(<CatalogEmptyLinks message="none" noSuites noAgents={false} />);
		expect(html).toContain('href="/catalog"');
		expect(html).toContain('href="/suites/new"');
		expect(html).not.toContain('href="/agents/new"');
	});

	it("an empty catalog offers New agent when agents are missing", () => {
		const html = render(<CatalogEmptyLinks message="none" noSuites={false} noAgents />);
		expect(html).toContain('href="/agents/new"');
		expect(html).not.toContain('href="/suites/new"');
	});
});