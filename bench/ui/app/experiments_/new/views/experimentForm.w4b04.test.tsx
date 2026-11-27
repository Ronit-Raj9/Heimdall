import "@/lib/i18n";

import type { Catalog } from "@/lib/types/bench";
import { mutation, render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { ExperimentForm } from "./experimentForm";
import { dateSeed, defaultFormValues } from "./experimentFormSchema";

const GATE =
	"plan e_x: arm A: skills: skill zz-good in agents/zz-test-loop/skills changed after its scan: open the agent's Tools tab and press Scan";

vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));
vi.mock("@/lib/store/apis/agentopsApi", () => ({ useScanAgentSkillMutation: () => mutation() }));
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	usePlanExperimentMutation: () => mutation({ error: { status: 422, data: { error: { message: GATE } } } }),
	useCreateExperimentMutation: () => mutation(),
	useStartExperimentMutation: () => mutation(),
	useEstimateExperimentMutation: () => mutation(),
}));

const catalog = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/catalog.json"), "utf8")) as Catalog;
const defaults = defaultFormValues(catalog, dateSeed(new Date("2026-11-18T06:00:00Z")));

describe("New comparison plan refusal (wave 4, N22)", () => {
	it("offers the skill scan when the skill gate refused the plan", () => {
		const html = render(<ExperimentForm catalog={catalog} defaults={defaults} onStarted={() => undefined} />);
		expect(html).toContain('data-testid="experiment-plan-skill-problem"');
		expect(html).not.toContain('data-testid="experiment-plan-error"');
	});
});