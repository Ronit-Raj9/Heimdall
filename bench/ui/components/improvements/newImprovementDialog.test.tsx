import "@/lib/i18n";

import { mutation, okQuery, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { NewImprovementDialog } from "./newImprovementDialog";

vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => okQuery({ agents: [] }),
	useGetSuitesQuery: () => {
		throw new Error("the dialog must not wait for the full benchmark list");
	},
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({
	useGetSuitesLightQuery: () =>
		okQuery({
			suites: [
				{ id: "hd-harbor-tb2", version: "2.0.0" },
				{ id: "hd-harbor-bfcl", version: "1.0.0" },
			],
		}),
}));
vi.mock("@/lib/store/apis/agentBuilderApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/agentBuilderApi")>()),
	useGetAgentConfigQuery: () => ({}),
}));
vi.mock("@/lib/store/apis/improveApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveApi")>()),
	useCreateImprovementMutation: () => mutation(),
	useEstimateImprovementMutation: () => mutation(),
}));

describe("NewImprovementDialog benchmark picker", () => {
	it("fills from the light benchmark list, sorted, with versions", () => {
		const html = render(<NewImprovementDialog onClose={() => undefined} />);
		// Q-109: the shared Select renders its options only when opened; the sorted choices are pinned in
		// newImprovementDialog.uf2b.test.ts. Here the styled trigger with its placeholder must show.
		expect(html).toMatch(/<button[^>]*data-testid="new-improvement-suite"/);
		expect(html).toContain('data-slot="select-value"');
	});
});
// Round 5 lane L7: the study create and chain check hooks, outside a store.
vi.mock("@/lib/store/apis/improveStudyApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/improveStudyApi")>()),
	useCreateImprovementStudyMutation: () => [() => ({ unwrap: async () => ({ id: "ims_x" }) }), { isLoading: false }],
	useLazyCheckImprovementLineageQuery: () => [() => undefined, { isFetching: false, isError: false }],
}));