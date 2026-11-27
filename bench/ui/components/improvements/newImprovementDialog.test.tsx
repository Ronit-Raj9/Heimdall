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
		const bfcl = html.indexOf("hd-harbor-bfcl@1.0.0");
		const tb2 = html.indexOf("hd-harbor-tb2@2.0.0");
		expect(bfcl).toBeGreaterThan(-1);
		expect(tb2).toBeGreaterThan(bfcl);
	});
});