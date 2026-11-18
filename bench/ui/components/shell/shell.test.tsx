import "@/lib/i18n";

import { render, tagOf } from "@/lib/testing/render";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { describe, expect, it, vi } from "vitest";
import { EmptyState } from "./emptyState";
import { EntityLink, entityHref, entityTarget } from "./entityLink";
import { PageHeader } from "./pageHeader";
import { RouteTabs, resolveTab, type RouteTab } from "./routeTabs";
import { StatusBadge, isDecision } from "./statusBadge";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("EntityLink", () => {
	it("builds one URL per object kind", () => {
		expect(entityHref("experiment", "e_1")).toBe("/experiments/e_1");
		expect(entityHref("run", "r_a b")).toBe("/runs/r_a%20b");
		expect(entityHref("suite", "coding-core")).toBe("/suites/coding-core");
		expect(entityHref("suite", "coding-core@1.0.0")).toBe("/suites/coding-core");
		expect(entityHref("task", "t1", { suite: "coding-core" })).toBe("/suites/coding-core?tab=tasks&task=t1");
		expect(entityHref("agent", "codex")).toBe("/agents?agent=codex");
	});
	it("has no target for a task without its suite or for an empty id", () => {
		expect(entityTarget("task", "t1")).toBeNull();
		expect(entityTarget("run", "")).toBeNull();
	});
	it("renders the id in mono as a link, with the full id in the title", () => {
		const html = render(<EntityLink kind="run" id="r_0123456789abcdef" short={8} testId="link" />);
		const tag = tagOf(html, "link") ?? "";
		expect(tag).toContain('href="/runs/r_0123456789abcdef"');
		expect(tag).toContain("font-mono");
		expect(tag).toContain('title="Open run r_0123456789abcdef"');
		expect(tag).toContain('aria-label="Open run r_0123456789abcdef"');
		expect(html).toContain("r_012345…");
	});
	it("shows a name in place of the id when given, and plain text when there is nothing to link", () => {
		expect(
			render(
				<EntityLink kind="experiment" id="e_1">
					Nightly
				</EntityLink>,
			),
		).toContain(">Nightly</a>");
		const plain = render(<EntityLink kind="task" id="t1" testId="plain" />);
		expect(plain).not.toContain("<a");
		expect(plain).toContain(">t1<");
	});
});

describe("StatusBadge", () => {
	it("labels each experiment state and pulses while running", () => {
		const running = render(<StatusBadge state="running" />);
		expect(running).toContain('data-state="running"');
		expect(running).toContain("Running");
		expect(running).toContain("animate-ping");
		expect(render(<StatusBadge state="done" />)).not.toContain("animate-ping");
	});
	it("gives each decision its word and icon, never green for a regression", () => {
		const regressed = render(<StatusBadge decision="regressed" />);
		expect(regressed).toContain("Regressed");
		expect(regressed).toContain("text-chart-error-ink");
		expect(regressed).not.toContain("chart-success");
		expect(regressed).toContain("<svg");
		expect(render(<StatusBadge decision="improved" />)).toContain("text-chart-success-ink");
	});
	it("shows an unknown value as a neutral badge and an empty decision as no verdict", () => {
		expect(render(<StatusBadge decision="" />)).toContain("No verdict");
		expect(render(<StatusBadge state="paused" />)).toContain(">paused<");
		expect(isDecision("improved")).toBe(true);
		expect(isDecision("ship")).toBe(false);
	});
});

describe("PageHeader", () => {
	it("renders the title, one subtitle, actions and a breadcrumb trail with the last crumb current", () => {
		const html = render(
			<PageHeader
				title="Experiment e_1"
				subtitle="One sentence."
				actions={<button type="button">Export</button>}
				breadcrumbs={[{ label: "Experiments", to: "/experiments" }, { label: "e_1" }]}
			/>,
		);
		expect(html).toContain("<h1");
		expect(html).toContain("Experiment e_1");
		expect(html).toContain("One sentence.");
		expect(html).toContain('aria-label="Breadcrumb"');
		expect(html).toContain('href="/experiments"');
		expect(html).toContain('aria-current="page"');
		expect(html).toContain(">Export</button>");
	});
});

describe("EmptyState", () => {
	it("renders one sentence and one action link", () => {
		const html = render(<EmptyState message="No runs yet." action={{ label: "New experiment", to: "/experiments/new" }} />);
		expect(html).toContain("No runs yet.");
		expect(html).toContain('href="/experiments/new"');
	});
});

describe("RouteTabs", () => {
	const tabs: RouteTab[] = [
		{ id: "verdict", label: "Verdict" },
		{ id: "matrix", label: "Matrix" },
		{ id: "adaptive", label: "Adaptive", hidden: true },
	];
	it("resolves the requested tab only when it is visible", () => {
		expect(resolveTab(tabs, "matrix")).toBe("matrix");
		expect(resolveTab(tabs, "adaptive")).toBe("verdict");
		expect(resolveTab(tabs, "nope", "matrix")).toBe("matrix");
		expect(resolveTab(tabs, null)).toBe("verdict");
		expect(resolveTab([{ id: "x", label: "X", hidden: true }], null)).toBeNull();
	});
	it("reads the tab from the URL, hides tabs that do not apply and renders only the active panel", () => {
		const html = render(
			<NuqsTestingAdapter searchParams="?tab=matrix">
				<RouteTabs tabs={tabs} label="Experiment views">
					{(active) => <p data-testid="panel">{active}</p>}
				</RouteTabs>
			</NuqsTestingAdapter>,
		);
		expect(html).toContain('role="tablist"');
		expect(html).toContain('aria-label="Experiment views"');
		expect(html).toContain('data-active="matrix"');
		expect(html).not.toContain("Adaptive");
		expect(html).toContain('<p data-testid="panel">matrix</p>');
		expect(tagOf(html, "tab-matrix")).toContain('aria-selected="true"');
		expect(tagOf(html, "tab-verdict")).toContain('aria-selected="false"');
	});
});