import "@/lib/i18n";

import type { Catalog } from "@/lib/types/bench";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { mutation, render } from "@/lib/testing/render";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { MatrixBuilder } from "./matrixBuilder";
import { defaultBuilderValues } from "./matrixBuilderSchema";

vi.mock("@/components/models/modelPicker", async () => await import("@/lib/testing/modelPickerStub"));
vi.mock("@/components/experiments/experiments.utils", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/experiments/experiments.utils")>()),
	readWriteToken: () => "",
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);
vi.mock("@/lib/store/apis/benchApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/benchApi")>()),
	usePlanExperimentMutation: () => mutation(),
	useCreateExperimentMutation: () => mutation(),
	useStartExperimentMutation: () => mutation(),
	useEstimateExperimentMutation: () => mutation(),
}));

const catalog = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../../../public/fixtures/catalog.json"), "utf8")) as Catalog;

describe("MatrixBuilder", () => {
	it("renders agents, models, the preview and estimate, plan and run with no YAML", () => {
		const defaults = {
			...defaultBuilderValues(catalog, 20261121),
			models: [
				{ provider: "anthropic" as const, model: "claude-haiku-4-5" },
				{ provider: "openai" as const, model: "gpt-5.4-mini" },
			],
		};
		const html = render(<MatrixBuilder catalog={catalog} defaults={defaults} onStarted={() => undefined} />, hostedSession("member"));
		expect(html).toContain('data-testid="builder-agent-claude-code"');
		expect(html).toContain('data-testid="builder-model-0"');
		expect(html).toContain('data-testid="builder-adaptive"');
		expect(html).toContain('data-testid="builder-arm-A"');
		expect(html).toContain("gpt-5.4-mini via openai");
		expect(html).toContain('data-testid="builder-estimate"');
		expect(html).toContain('data-testid="builder-plan"');
		// Run waits for a plan.
		expect(html).toMatch(/data-testid="builder-run"[^>]*disabled=""/);
		expect(html).toContain("Plan the experiment first");
		expect(html).not.toContain("builder-write-token");
	});

	it("lays the form out as four steps with Extras collapsed and a summary card", () => {
		const html = render(
			<MatrixBuilder catalog={catalog} defaults={defaultBuilderValues(catalog, 1)} onStarted={() => undefined} />,
			hostedSession("member"),
		);
		for (const n of [1, 2, 3]) expect(html).toContain(`data-testid="builder-step-${n}"`);
		expect(html).toMatch(/<details[^>]*data-testid="builder-extras"/);
		expect(html).not.toMatch(/<details[^>]*open=""[^>]*data-testid="builder-extras"/);
		const summary = html.slice(html.indexOf('data-testid="builder-summary"'));
		expect(summary).toContain('data-testid="builder-preview"');
		expect(summary).toContain('data-testid="builder-plan"');
		expect(summary).toContain('data-testid="builder-run"');
	});

	it("asks for the write token in local mode before estimate and plan", () => {
		const html = render(<MatrixBuilder catalog={catalog} defaults={defaultBuilderValues(catalog, 1)} onStarted={() => undefined} />);
		expect(html).toContain('data-testid="builder-write-token"');
		expect(html).toMatch(/data-testid="builder-plan"[^>]*disabled=""/);
	});
});