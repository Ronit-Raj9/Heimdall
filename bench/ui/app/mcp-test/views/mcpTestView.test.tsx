import { mutation, okQuery, render } from "@/lib/testing/render";
import type { McpReport } from "@/lib/types/mcptest";
import { scoreBand, serverIdFrom } from "@/lib/types/mcptest";
import { hostedSession } from "@/lib/testing/platformFixtures";
import { skipToken } from "@reduxjs/toolkit/query";
import { describe, expect, it, vi } from "vitest";
import { defaultServerId, DraftEvaluateLink, EMPTY_FORM, McpReportView, McpTestView, requestFromForm } from "./mcpTestView";

vi.mock("@/lib/store/apis/mcpTestApi", () => ({
	useInspectMcpServerMutation: () => mutation(),
	useDraftMcpBenchmarkMutation: () => mutation(),
}));
const secretsQuery = vi.hoisted(() => vi.fn());
vi.mock("@/lib/store/apis/secretsApi", () => ({
	useListSecretsQuery: (arg: unknown) => {
		secretsQuery(arg);
		return okQuery({ secrets: [{ id: "k1", name: "SHOP_MCP_TOKEN", created_at: "", updated_at: "" }] });
	},
}));
vi.mock("@tanstack/react-router", async () => ({ ...(await import("@/lib/testing/routerMock")).routerMock, useBlocker: () => undefined }));

const report: McpReport = {
	url: "https://mcp.shop.example/mcp",
	checked_at: "2026-11-22T00:00:00Z",
	reached: true,
	server: { protocol_version: "2025-06-18", name: "Fake Shop", version: "1.2.0", has_tools: true, session: true },
	tools: [
		{ name: "search_orders", description: "Search orders.", read_only: true, destructive: false },
		{ name: "do", description: "", read_only: false, destructive: false },
	],
	scores: { overall: 71, reliability: 100, error_recovery: 86, latency: 100, schema: 60, description: 10 },
	reliability: { attempts: 9, succeeded: 9, rate: 1, stable_listing: false, ping_supported: true, failures: [] },
	latency: { samples: 20, p50_ms: 3, p95_ms: 9, max_ms: 12, by_method: {} },
	error_recovery: [
		{ id: "unknown_tool", title: "Call to a tool that does not exist", passed: true, detail: "refused" },
		{ id: "wrong_types", title: "Wrong types", passed: false, skipped: true, detail: "no tool may be called" },
	],
	calls: [{ tool: "search_orders", args: { query: "test" }, placeholder: true, ok: true, is_error: false, latency_ms: 4, detail: "ok" }],
	quality: {
		schema_score: 60,
		description_score: 10,
		tools: [
			{
				tool: "search_orders",
				description_score: 100,
				schema_score: 100,
				params: 2,
				required: 1,
				read_only: true,
				destructive: false,
				issues: [],
			},
			{
				tool: "do",
				description_score: 0,
				schema_score: 30,
				params: 1,
				required: 0,
				read_only: false,
				destructive: false,
				issues: [{ code: "description_missing", severity: "warn", message: "The tool has no description.", fix: "Say what it does." }],
			},
		],
		server_issues: [],
	},
	scan: [],
	requests: 31,
};

describe("mcp test helpers", () => {
	it("builds the inspect request from the form", () => {
		expect(requestFromForm({ ...EMPTY_FORM, url: " https://x.example/mcp " })).toEqual({
			url: "https://x.example/mcp",
			call_read_only: true,
		});
		expect(requestFromForm({ ...EMPTY_FORM, url: "u", secret: "T" })).toEqual({
			url: "u",
			call_read_only: true,
			secret: "T",
			auth_scheme: "Bearer",
		});
		expect(requestFromForm({ ...EMPTY_FORM, url: "u", secret: "T", mode: "header", header: " X-Api-Key " })).toEqual({
			url: "u",
			call_read_only: true,
			secret: "T",
			auth_header: "X-Api-Key",
		});
		expect(requestFromForm({ ...EMPTY_FORM, url: "u", secret: "T", mode: "raw" })).toEqual({ url: "u", call_read_only: true, secret: "T" });
	});

	it("names the server and bands scores", () => {
		expect(defaultServerId(report)).toBe("fake-shop");
		expect(defaultServerId({ ...report, server: { ...report.server, name: "" } })).toBe("mcp");
		expect(serverIdFrom("  My_Server!! ")).toBe("my-server");
		expect([scoreBand(80), scoreBand(50), scoreBand(49)]).toEqual(["good", "fair", "poor"]);
	});
});

describe("McpReportView", () => {
	it("shows scores, error handling, tool issues and warnings", () => {
		const html = render(<McpReportView report={report} />);
		expect(html).toContain('data-testid="mcp-score-overall"');
		expect(html).toContain("Call to a tool that does not exist");
		expect(html).toContain("The tool has no description.");
		expect(html).toContain("The tool list changed");
		expect(html).toContain('data-testid="mcp-tool-do"');
		expect(html).toContain("sample inputs from the schema");
		expect(html).not.toMatch(/suite|runner|probe|\u2014/i);
	});

	it("says why an unreachable server has no report", () => {
		const html = render(<McpReportView report={{ ...report, reached: false, error: "initialize: HTTP 401" }} />);
		expect(html).toContain("initialize: HTTP 401");
		expect(html).not.toContain('data-testid="mcp-report"');
	});
});

describe("McpTestView", () => {
	it("renders the form with org secrets", () => {
		const html = render(<McpTestView />);
		expect(html).toContain("Test my MCP server");
		expect(html).toContain("SHOP_MCP_TOKEN");
		expect(html).toContain('data-testid="mcp-inspect"');
	});
});
// J9.secrets: viewers get no list of the organization's secret names.
describe("MCP test credential field", () => {
	it("does not ask for the secret names for a viewer and offers a free-text field", () => {
		secretsQuery.mockClear();
		const html = render(<McpTestView />, hostedSession("viewer"));
		expect(secretsQuery).toHaveBeenCalledWith(skipToken);
		expect(html).not.toContain("SHOP_MCP_TOKEN");
		expect(html).toContain('data-testid="mcp-secret-name"');
		expect(html).toContain("Secret name");
		expect(html).not.toContain('data-testid="mcp-secret"');
	});
	it("lists the secret names for a member", () => {
		secretsQuery.mockClear();
		const html = render(<McpTestView />, hostedSession("member"));
		expect(secretsQuery).not.toHaveBeenCalledWith(skipToken);
		expect(html).toContain("SHOP_MCP_TOKEN");
		expect(html).toContain('data-testid="mcp-secret"');
	});
});
describe("DraftEvaluateLink (N113)", () => {
	const done = (keyed?: string[]) => ({
		suite: "zz-test-mcp",
		tasks: [],
		no_reference: [],
		files: [],
		scores: report.scores,
		reference_agents: ["codex", "claude-code", "opencode"],
		...(keyed ? { reference_agents_keyed: keyed } : {}),
		next: "",
	});

	it("opens a one-agent evaluation with the only keyed agent", () => {
		const html = render(<DraftEvaluateLink done={done(["codex"])} />);
		expect(html).toContain('href="/evaluate?suite=zz-test-mcp&amp;agent=codex"');
		expect(html).toContain("Evaluate with codex");
		expect(html).not.toContain("claude-code");
	});

	it("compares two keyed agents in opponent mode", () => {
		const html = render(<DraftEvaluateLink done={done(["codex", "claude-code"])} />);
		expect(html).toContain('href="/evaluate?suite=zz-test-mcp&amp;agent=codex&amp;mode=opponent&amp;opponent=claude-code"');
		expect(html).toContain("Evaluate with codex and claude-code");
	});

	it("explains the missing key and links Settings, Model keys, when no agent has one", () => {
		const html = render(<DraftEvaluateLink done={done()} />);
		expect(html).not.toContain('data-testid="mcp-draft-evaluate"');
		expect(html).toContain("None of the reference agents (claude-code, codex, opencode) has a model key in this organization.");
		expect(html).toContain('href="/settings?tab=keys&amp;return=%2Fmcp-test"');
		expect(html).toContain("Settings &gt; Model keys");
	});
});