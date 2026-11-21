import "@/lib/i18n";

import ExperimentListView from "@/app/experiments/views/experimentListView";
import { NewSuiteButton } from "@/app/suites/views/suitesView";
import { demoSession, hostedSession } from "@/lib/testing/platformFixtures";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { CatalogEmptyLinks } from "./catalogEmptyLinks";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/experiments/experimentList", () => ({ ExperimentList: () => <div data-testid="experiment-list" /> }));

describe("create links", () => {
	it("suites list has New suite linking to /suites/new", () => {
		const html = render(<NewSuiteButton />);
		expect(html).toContain('href="/suites/new"');
		expect(html).toContain("New suite");
	});

	it("experiments list has New experiment for members only", () => {
		const html = render(<ExperimentListView />, hostedSession("member"));
		expect(html).toContain('data-testid="experiments-new"');
		expect(html).toContain('href="/experiments/new"');
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
