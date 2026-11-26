import "@/lib/i18n";

import { mutation, render } from "@/lib/testing/render";
import { describe, expect, it, vi } from "vitest";
import { QuickRunDialog } from "./quickRunDialog";

const GATE =
	"plan e_x: arm A: skills: skill zz-good in agents/zz-test-loop/skills has no passing scan: open the agent's Tools tab and press Scan";

vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/components/models/modelPicker", () => ({ ModelPicker: ({ testId }: { testId: string }) => <div data-testid={testId} /> }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	useGetAgentsQuery: () => ({ data: { agents: [] }, isSuccess: true }),
}));
vi.mock("@/lib/store/apis/suitesLightApi", () => ({ useGetSuitesLightQuery: () => ({ data: { suites: [] }, isSuccess: true }) }));
vi.mock("@/lib/store/apis/agentopsApi", () => ({ useScanAgentSkillMutation: () => mutation() }));
vi.mock("@/lib/store/apis/quickrunApi", () => ({
	useQuickRunMutation: () => mutation({ error: { status: 422, data: { error: { message: GATE } } } }),
}));

describe("quick test dialog plan refusal (wave 4, N22)", () => {
	it("offers the skill scan when the skill gate refused the plan", () => {
		const html = render(
			<QuickRunDialog suite="s" agent="zz-test-loop" model={{ provider: "openai", model: "m" }} tasks={5} onClose={() => undefined} />,
		);
		expect(html).toContain('data-testid="quickrun-skill-problem"');
		expect(html).not.toContain('data-testid="quickrun-error"');
	});
});