import type { OnboardInventory, OnboardPlan } from "@/lib/types/onboard";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

// The light list of a hosted server: coding-core is hidden from suites and named in reserved_ids (N46).
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () => ({
		data: { suites: [{ id: "faq", version: "1", task_count: 3 }], reserved_ids: ["coding-core"] },
		isSuccess: true,
	}),
}));

const { InventoryPanel } = await import("./inventoryPanel");

const inv = {
	kind: "repo",
	source: "repo",
	title: "tasks",
	license: "",
	unit: "task",
	units: 2,
	samples: [],
	suggest: { source: "repo", suite: "tasks" },
	estimate: { tasks: 2, readiness_runs: 6, judge_calls: 0, assist_usd: 0 },
	can_draft: true,
	next: "",
} as unknown as OnboardInventory;

function render(suite: string) {
	const plan = { source: "repo", suite } as unknown as OnboardPlan;
	return renderToStaticMarkup(<InventoryPanel inv={inv} plan={plan} onPlan={() => undefined} onDraft={() => undefined} busy={false} />);
}

function draftButton(html: string) {
	return html.match(/<button[^>]*data-testid="onboard-draft"[^>]*>/)?.[0] ?? "";
}

describe("Add benchmark id check against reserved_ids", () => {
	it("refuses coding-core before sending: Draft disabled, the built-in line shown", () => {
		const html = render("coding-core");
		expect(html).toContain('data-state="builtin"');
		expect(draftButton(html)).toContain('disabled=""');
	});
	it("keeps Draft enabled for a new id", () => {
		const html = render("my-new-suite");
		expect(html).toContain('data-state="new"');
		expect(draftButton(html)).not.toContain('disabled=""');
	});
});