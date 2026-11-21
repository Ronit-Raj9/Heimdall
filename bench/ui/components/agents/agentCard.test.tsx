import "@/lib/i18n";

import type { AgentList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AgentCard } from "./agentCard";

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/agents.json"), "utf8")) as AgentList;
const agent = (id: string) => list.agents.find((a) => a.id === id)!;

describe("AgentCard", () => {
	it("shows env var names and file paths, the per-suite build and the evidence level", () => {
		const html = renderToStaticMarkup(<AgentCard agent={agent("acme-langgraph")} />);
		expect(html).toContain(">BENCH_API_KEY<");
		expect(html).toContain(">/opt/bench/launch.sh<");
		expect(html).toContain("per benchmark, on SUITE_BASE");
		expect(html).toContain("gateway only");
		expect(html).toMatch(/data-testid="agents-card-acme-langgraph-runs"[\s\S]*?>12</);
	});

	it("marks a native parser, a scripted control and fallbacks", () => {
		expect(renderToStaticMarkup(<AgentCard agent={agent("codex")} />)).toContain("native parser codex");
		expect(renderToStaticMarkup(<AgentCard agent={agent("noop")} />)).toContain("scripted noop");
		expect(renderToStaticMarkup(<AgentCard agent={agent("custom-python")} />)).toContain("anthropic, fallbacks: openai");
	});

	it("shows a config that failed to load with its error", () => {
		const broken = list.agents.find((a) => a.error)!;
		const html = renderToStaticMarkup(<AgentCard agent={broken} />);
		expect(html).toContain('data-state="error"');
		expect(html).toContain("budgets.wall_s: required field missing");
	});

	it("says unknown, never 0, when the run count is not known", () => {
		const html = renderToStaticMarkup(<AgentCard agent={{ ...agent("codex"), sealed_runs: null }} />);
		expect(html).toMatch(/data-testid="agents-card-codex-runs"[\s\S]*?unknown/);
	});
});