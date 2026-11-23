import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it } from "vitest";
import { aliasTarget, RouteTabs, resolveTab, unknownTab, type RouteTab } from "./routeTabs";

const tabs: RouteTab[] = [
	{ id: "report-card", label: "Report card" },
	{ id: "runs", label: "Runs" },
	{ id: "checks", label: "Readiness check", hidden: true, addressable: true },
	{ id: "secret", label: "Secret", hidden: true },
];

describe("addressable hidden tabs", () => {
	it("resolves a hidden tab that is addressable, never a hidden one that is not", () => {
		expect(resolveTab(tabs, "checks")).toBe("checks");
		expect(resolveTab(tabs, "secret")).toBe("report-card");
		expect(resolveTab(tabs, "secret", "runs")).toBe("runs");
		// An addressable tab is never the default or the fallback.
		expect(resolveTab(tabs, null)).toBe("report-card");
		expect(resolveTab(tabs, null, "checks")).toBe("report-card");
	});

	it("shows the active addressable tab at the end of the strip, and only while it is active", () => {
		const page = (search: string) =>
			render(
				<NuqsTestingAdapter searchParams={search}>
					<RouteTabs tabs={tabs} label="Agent views">
						{(active) => <p data-testid="panel">{active}</p>}
					</RouteTabs>
				</NuqsTestingAdapter>,
			);
		const open = page("?tab=checks");
		expect(open).toContain('data-active="checks"');
		expect(open).toContain('<p data-testid="panel">checks</p>');
		expect(tagOf(open, "tab-checks")).toContain('aria-selected="true"');
		expect(open.indexOf('data-testid="tab-runs"')).toBeLessThan(open.indexOf('data-testid="tab-checks"'));
		expect(open).not.toContain("Secret");

		const other = page("?tab=runs");
		expect(other).toContain('data-active="runs"');
		expect(other).not.toContain('data-testid="tab-checks"');
	});
});
describe("unknown tab in the address (N76)", () => {
	const page = (search: string) =>
		render(
			<NuqsTestingAdapter searchParams={search}>
				<RouteTabs tabs={tabs} label="Agent views">
					{(active) => <p data-testid="panel">{active}</p>}
				</RouteTabs>
			</NuqsTestingAdapter>,
		);

	it("says the page has no such tab and which tab is shown instead", () => {
		const html = page("?tab=bogus");
		expect(html).toContain('data-active="report-card"');
		expect(html).toContain('data-testid="route-tabs-unknown"');
		expect(html).toContain("This page has no bogus tab, so Report card is shown.");
		expect(html).toContain('role="status"');
	});

	it("adds no note for a known tab, a hidden known tab or no tab", () => {
		for (const search of ["?tab=runs", "?tab=secret", "?tab=checks", ""]) {
			expect(page(search)).not.toContain('data-testid="route-tabs-unknown"');
		}
	});
});
describe("tab aliases (N271)", () => {
	const aliased: RouteTab[] = [
		{ id: "tasks", label: "Tasks" },
		{ id: "edit", label: "Edit files", aliases: ["files"] },
	];
	const page = (search: string) =>
		render(
			<NuqsTestingAdapter searchParams={search}>
				<RouteTabs tabs={aliased} label="Benchmark views">
					{(active) => <p data-testid="panel">{active}</p>}
				</RouteTabs>
			</NuqsTestingAdapter>,
		);

	it("opens the tab an alias names, without the unknown-tab note", () => {
		const html = page("?tab=files");
		expect(html).toContain('data-active="edit"');
		expect(html).toContain('<p data-testid="panel">edit</p>');
		expect(html).not.toContain('data-testid="route-tabs-unknown"');
	});

	it("rewrites the address to the real tab id", () => {
		expect(aliasTarget(aliased, "files")).toBe("edit");
		expect(aliasTarget(aliased, "edit")).toBeNull();
		expect(aliasTarget(aliased, "bogus")).toBeNull();
		expect(resolveTab(aliased, "files")).toBe("edit");
		expect(unknownTab(aliased, "files")).toBeNull();
		expect(unknownTab(aliased, "bogus")).toBe("bogus");
	});
});