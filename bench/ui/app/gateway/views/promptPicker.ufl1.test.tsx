import "@/lib/i18n";

import { render } from "@/lib/testing/render";
import type { GwEffect } from "@/lib/types/gateway";
import { describe, expect, it, vi } from "vitest";
import { draftRequest } from "./featureTestDialog";
import { PastResultsTable, effectDate } from "./pastResults";
import { PromptPick, needsConnect, versionLabel } from "./promptPicker";

const state = vi.hoisted(() => ({ prompts: {} as unknown }));
vi.mock("@/lib/store/apis/gatewayPromptsApi", () => ({
	useGetGatewayPromptsQuery: () => state.prompts,
	useGetGatewayPromptVersionsQuery: () => ({ data: undefined }),
	useGetGatewayEffectsQuery: () => ({ data: undefined }),
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
	useNavigate: () => () => undefined,
}));

describe("prompt version test picker (round 5 L1)", () => {
	it("sends the body the Go draft decodes (gwfeature/prompt_ufl1_test.go)", () => {
		const { req } = draftRequest("prompt_version", {
			suite: "s@1",
			base: "agents/a.yaml",
			model: "",
			provider: "",
			budget: "5",
			trials: "2",
			options: { prompt_id: "pr-9", variant_version: "4", control_version: "3" },
		});
		expect(JSON.stringify(req)).toBe(
			`{"feature":"prompt_version","suite":"s@1","base":"agents/a.yaml","trials":2,"budget_usd":5,"options":{"prompt_id":"pr-9","variant_version":"4","control_version":"3"}}`,
		);
	});

	it("says to connect Bifrost on a 409 and stays disabled", () => {
		state.prompts = { error: { status: 409, data: { error: { message: "Connect your Bifrost first" } } } };
		expect(needsConnect(state.prompts && (state.prompts as { error: unknown }).error)).toBe(true);
		const html = render(<PromptPick id="p" value="" onChange={() => undefined} />);
		expect(html).toContain("Connect your Bifrost to list its prompts.");
		expect(html).toContain("disabled");
	});

	it("shows the server's reason on other errors", () => {
		state.prompts = { error: { status: 502, data: { error: { message: "Heimdall could not read from your Bifrost: timeout" } } } };
		expect(render(<PromptPick id="p" value="" onChange={() => undefined} />)).toContain("timeout");
	});

	it("labels a version with its number and message", () => {
		expect(versionLabel({ version: 4, commit_message: "shorter", created_at: "", messages: 1, model: "" })).toBe("v4 · shorter");
	});
});

describe("past results", () => {
	it("lists feature, comparison, effect and date", () => {
		const e = {
			feature: "semantic_cache",
			exp_id: "exp-1",
			result: "saved",
			headline: "Saved 12% cost",
			computed_at: "2026-11-20T10:00:00Z",
			arms: [],
		} as unknown as GwEffect;
		const html = render(<PastResultsTable effects={[e]} names={{ semantic_cache: "Semantic cache" }} />);
		expect(html).toContain("Semantic cache");
		expect(html).toContain("exp-1");
		expect(html).toContain("Saved 12% cost");
		expect(effectDate("0001-01-01T00:00:00Z")).toBe("");
	});
});