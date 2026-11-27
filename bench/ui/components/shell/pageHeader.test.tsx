import "@/lib/i18n";

import { routerState } from "@/lib/testing/routerMock";
import { render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { PageHeader } from "./pageHeader";

// Fix wave 3, N203: the top bar already names the section, so the page trail does not repeat it, and
// every item of the trail has text (the chevrons sit inside the items, between them only).

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

describe("PageHeader breadcrumbs", () => {
	it("drops a leading crumb that repeats the section in the top bar", () => {
		routerState.location.pathname = "/experiments/e_x";
		const html = render(
			<PageHeader
				title="zz-test"
				breadcrumbs={[
					{ label: "Comparisons", to: "/experiments" },
					{ label: "Evaluation: codex", to: "/evaluations/$evalId", params: { evalId: "ev_1" } },
					{ label: "zz-test" },
				]}
			/>,
		);
		expect(html).not.toContain(">Comparisons<");
		expect(html).toContain('href="/evaluations/ev_1"');
		expect(html.match(/<li[^>]*>\s*<\/li>/g) ?? []).toEqual([]);
		expect(html.match(/<li/g)?.length).toBe(2);
		expect(html.match(/lucide-chevron-right/g)?.length).toBe(1);
	});
	it("shows no trail when only the current page would be left", () => {
		routerState.location.pathname = "/experiments/e_x";
		const html = render(<PageHeader title="zz-test" breadcrumbs={[{ label: "Comparisons", to: "/experiments" }, { label: "zz-test" }]} />);
		expect(html).not.toContain("<nav");
	});
	it("keeps a crumb to another section", () => {
		routerState.location.pathname = "/runs/r_1";
		const html = render(<PageHeader title="r_1" breadcrumbs={[{ label: "Comparisons", to: "/experiments" }, { label: "r_1" }]} />);
		expect(html).toContain('href="/experiments"');
		expect(html.match(/lucide-chevron-right/g)?.length).toBe(1);
		routerState.location.pathname = "/verdict";
	});
});
describe("PageHeader breadcrumbs on phones (Q-63)", () => {
	it("hides every crumb but the last below sm", () => {
		const html = render(
			<PageHeader
				title="r_1"
				breadcrumbs={[
					{ label: "Runs", to: "/runs" },
					{ label: "Evaluation e_1", to: "/evaluations" },
					{ label: "r_1" },
				]}
			/>,
		);
		const items = html.match(/<li class="[^"]*"/g) ?? [];
		expect(items.length).toBeGreaterThan(1);
		expect(items.slice(0, -1).every((li) => li.includes("max-sm:hidden"))).toBe(true);
		expect(items.at(-1)).not.toContain("max-sm:hidden");
	});
});
