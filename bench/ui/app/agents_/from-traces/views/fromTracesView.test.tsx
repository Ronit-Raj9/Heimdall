import "@/lib/i18n";

import type { TraceConnectInfo } from "@/lib/types/connectTraces";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { connectRequest, withToken } from "@/lib/store/apis/connectTracesHelpers";
import FromTracesView from "./fromTracesView";

const base: TraceConnectInfo = {
	endpoint: "https://h.example/api/bench/orgs/org_a/otlp/v1/traces",
	base: "https://h.example/api/bench/orgs/org_a/otlp",
	hosted: true,
	token_hint: "an ingest token of this org",
	setup: [{ id: "otel", label: "OpenTelemetry SDK", install: "pip install x", env: "OTEL_EXPORTER_OTLP_TRACES_HEADERS=Authorization=Bearer%20$HEIMDALL_INGEST_TOKEN" }],
	exports: 0,
	spans_kept: 0,
};

let data: TraceConnectInfo = base;

vi.mock("@/lib/store/apis/connectTracesApi", async () => ({
	...(await import("@/lib/store/apis/connectTracesHelpers")),
	useGetConnectTracesQuery: () => ({ data, isError: false, refetch: () => undefined }),
	useForgetConnectTracesMutation: () => [vi.fn(), { isLoading: false }],
	useInferFromTraceFileMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/liveinboxApi", () => ({ useCreateInboxTokenMutation: () => [vi.fn(), { isLoading: false, isError: false }] }));
vi.mock("@/lib/store/apis/agentBuilderApi", () => ({ useCreateAgentFromTemplateMutation: () => [vi.fn(), { isLoading: false, isError: false }] }));
vi.mock("@/lib/store/apis/benchApi", () => ({ getBenchErrorMessage: () => "err" }));
vi.mock("@/lib/session/sessionContext", () => ({ canUse: () => true, useSession: () => ({}) }));
vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => vi.fn(),
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}));

describe("FromTracesView", () => {
	beforeEach(() => {
		data = base;
	});

	it("shows the endpoint and setup while waiting for the first trace", () => {
		const html = renderToStaticMarkup(<FromTracesView />);
		expect(html).toContain("Connect from traces");
		expect(html).toContain("https://h.example/api/bench/orgs/org_a/otlp/v1/traces");
		expect(html).toContain("Create an ingest token");
		expect(html).toContain('data-testid="from-traces-waiting"');
		expect(html).not.toContain('data-testid="from-traces-connect"');
	});

	it("shows the inferred agent with evidence, questions and the connect form", () => {
		data = {
			...base,
			exports: 2,
			spans_kept: 8,
			summary: "support-bot, langgraph, gpt-5.4-mini, tools seen: lookup_order, served at POST /chat",
			agent_yaml: "version: 1\nid: support-bot\n",
			draft: {
				id: "support-bot",
				name: "support-bot",
				traces: 2,
				spans: 8,
				confidence: "high",
				template: "http-url",
				params: { url: "https://support.example.com/chat" },
				model: { provider: "openai", model: "gpt-5.4-mini" },
				tools: [{ name: "lookup_order", calls: 2, errors: 1 }],
				packs: ["support"],
				evidence: [{ field: "model", value: "gpt-5.4-mini", from: "gen_ai.request.model" }],
				questions: ["Does support.example.com need a token?"],
			},
		};
		const html = renderToStaticMarkup(<FromTracesView />);
		expect(html).toContain("support-bot, langgraph, gpt-5.4-mini");
		expect(html).toContain("Confidence: high");
		expect(html).toContain("gen_ai.request.model");
		expect(html).toContain("1 faults");
		expect(html).toContain("Does support.example.com need a token?");
		expect(html).toContain('value="https://support.example.com/chat"');
		expect(html).toContain("Connect this agent");
		expect(html).not.toMatch(/canary|probe|sealed|suite|runner|\u2014/i);
	});
});

describe("connect helpers", () => {
	it("builds the request from the draft and the edits", () => {
		const draft = { template: "http-url", params: { url: "x" }, model: { provider: "openai", model: "m" } };
		expect(connectRequest(draft, { id: "bot", url: "https://bot.example.com/run", authSecret: "BOT_TOKEN" })).toEqual({
			template: "http-url",
			id: "bot",
			params: { url: "https://bot.example.com/run", auth_secret: "BOT_TOKEN" },
			model: { provider: "openai", model: "m" },
		});
		expect(connectRequest(draft, { id: "Bad", url: "https://x.io", authSecret: "" })).toBeNull();
		expect(connectRequest(draft, { id: "bot", url: "ftp://x", authSecret: "" })).toBeNull();
		expect(connectRequest(draft, { id: "bot", url: "https://x.io", authSecret: "sk-not a name" })).toBeNull();
		expect(connectRequest({ template: "openai-endpoint" }, { id: "bot", url: "https://x.io/v1", authSecret: "K" })?.params).toEqual({ url: "https://x.io/v1", api_key_secret: "K" });
		expect(connectRequest({ template: "a2a-card" }, { id: "bot", url: "https://x.io/.well-known/agent-card.json", authSecret: "K" })?.params).toEqual({
			card_url: "https://x.io/.well-known/agent-card.json",
		});
	});

	it("fills the token into setup lines", () => {
		expect(withToken("A=$HEIMDALL_INGEST_TOKEN", "hmi_x")).toBe("A=hmi_x");
		expect(withToken("A=$HEIMDALL_INGEST_TOKEN", null)).toBe("A=$HEIMDALL_INGEST_TOKEN");
	});
});
