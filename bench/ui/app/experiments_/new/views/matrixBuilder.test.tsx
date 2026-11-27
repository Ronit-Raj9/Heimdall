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
vi.mock("@/components/models/groupedModelPicker", () => ({
	GroupedModelPicker: ({ testId }: { testId?: string }) => <div data-testid={testId} />,
}));
vi.mock("@/components/experiments/experiments.utils", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/components/experiments/experiments.utils")>()),
	readWriteToken: () => "",
}));
vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));
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

	it("lists only the agents My agents shows, built-in agents in their own group (J7.new-default)", () => {
		const withDemo: Catalog = {
			...catalog,
			agents: [
				...catalog.agents,
				{ id: "codex", path: "agents/codex.yaml", adapter: "command", model: "gpt-5.4-mini" },
				{ id: "acme-agent", path: "agents/acme-agent.yaml", adapter: "command", model: "claude-sonnet-4-5" },
				{ id: "storm", path: "agents/storm.yaml", adapter: "command", model: "" },
				{ id: "noop", path: "agents/noop.yaml", adapter: "scripted", model: "" },
			],
		};
		const html = render(
			<MatrixBuilder
				catalog={withDemo}
				defaults={defaultBuilderValues(withDemo, 1, { agent: "codex" })}
				agentGroups={{ yours: [], baselines: ["codex", "claude-code"] }}
				onStarted={() => undefined}
			/>,
			hostedSession("member"),
		);
		expect(html).toContain('data-testid="builder-agent-codex"');
		expect(html).toContain('data-testid="builder-agent-claude-code"');
		expect(html).toContain('data-testid="builder-agents-builtin"');
		expect(html).toContain("Built-in agents");
		for (const id of ["acme-agent", "storm", "noop"]) expect(html).not.toContain(`data-testid="builder-agent-${id}"`);
	});
});