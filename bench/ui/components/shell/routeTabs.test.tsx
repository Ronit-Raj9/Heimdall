import { render, tagOf } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it } from "vitest";
import { RouteTabs, resolveTab, type RouteTab } from "./routeTabs";

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