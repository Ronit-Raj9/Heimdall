import "@/lib/i18n";

import type { AgentTemplate, EndpointTestResult } from "@/lib/types/agentBuilder";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import {
	capText,
	DEFAULT_PULL_OPTIONS,
	defaultParams,
	detectFramework,
	EndpointPreviewView,
	EndpointTestPanel,
	missingParams,
	optionLabel,
	ParamFields,
	paramVisible,
	picksOwnModel,
	PULL_TEMPLATE,
	pullAgentYAML,
	PullOptionsFields,
	RAW_CAP,
	testable,
	withPullTemplate,
} from "./connectForm";

const role = vi.hoisted(() => ({ admin: true }));
vi.mock("@/lib/store/apis/benchApi", () => ({ getBenchErrorMessage: () => "boom" }));
vi.mock("@/lib/store/apis/agentBuilderApi", () => ({
	useTestEndpointMutation: () => [vi.fn(), { isLoading: false }],
	usePreviewEndpointMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/secretsApi", () => ({ useCreateSecretMutation: () => [vi.fn(), { isLoading: false }] }));
vi.mock("@/lib/session/sessionContext", () => ({ canUse: () => role.admin, useSession: () => ({}) }));

const http: AgentTemplate = {
	id: "http-url",
	name: "HTTP",
	category: "bring-your-own",
	description: "",
	adapter: "remote_http",
	providers: ["openai"],
	default_model: "m",
	builds: false,
	params: [
		{ name: "url", label: "URL", required: true },
		{ name: "tools", label: "Tools", required: false, kind: "bool", default: "false" },
		{ name: "timeout_s", label: "Timeout", required: false, kind: "number", default: "600" },
	],
};
const wrap: AgentTemplate = { ...http, id: "wrap-app", adapter: "command", params: [{ name: "command", label: "Cmd", required: true }] };

describe("connect form helpers", () => {
	it("pre-fills defaults", () => {
		expect(defaultParams(http)).toEqual({ tools: "false", timeout_s: "600" });
	});

	it("hides the model for agents that pick their own", () => {
		expect(picksOwnModel(http, {})).toBe(true);
		expect(picksOwnModel(http, { model_traffic: "true" })).toBe(false);
		expect(picksOwnModel({ ...http, adapter: "a2a" }, {})).toBe(true);
		expect(picksOwnModel({ ...http, adapter: "openai_agent" }, {})).toBe(false);
		expect(testable(http) && testable({ ...http, adapter: "openai_agent" }) && !testable(wrap)).toBe(true);
		expect(testable({ ...http, adapter: "a2a" })).toBe(true); // Y03: A2A agents are tested straight from their card
	});

	it("needs a repository or an image to wrap an app", () => {
		expect(missingParams(http, {})).toEqual(["url"]);
		expect(missingParams(wrap, { command: "x" })).toEqual(["repo_url"]);
		expect(missingParams(wrap, { command: "x", image: "a:1" })).toEqual([]);
	});

	it("guesses the framework", () => {
		expect(detectFramework({ repo_url: "https://github.com/o/langgraph-support" })).toBe("langgraph");
		expect(detectFramework({ command: "python -m crewai_app" })).toBe("crewai");
		expect(detectFramework({ image: "ghcr.io/o/openai-agents-bot:1" })).toBe("openai-agents");
		expect(detectFramework({ command: "uvicorn app:api" })).toBeNull();
	});
});

describe("EndpointTestPanel", () => {
	const ok: EndpointTestResult = {
		ok: true,
		matched: true,
		adapter: "remote_http",
		url: "https://a/run",
		status: 200,
		latency_ms: 840,
		answer: "READY",
		trace: [
			{ at_ms: 0, kind: "request", detail: "POST https://a/run" },
			{ at_ms: 840, kind: "answer", detail: "READY" },
		],
		notes: ["A test carries no tools block"],
	};

	it("shows the answer, latency and trace", () => {
		const html = renderToStaticMarkup(<EndpointTestPanel result={ok} loading={false} />);
		expect(html).toContain("Your endpoint answered in 840 ms");
		expect(html).toContain('data-testid="endpoint-test-answer"');
		expect(html).toContain("POST https://a/run");
		expect(html).toContain("HTTP 200");
		expect(html).toContain("A test carries no tools block");
	});

	it("shows a failure and a request error", () => {
		const html = renderToStaticMarkup(<EndpointTestPanel result={{ ...ok, ok: false, answer: undefined, error: "the endpoint answered 401: no" }} loading={false} />);
		expect(html).toContain("The test failed");
		expect(html).toContain("answered 401");
		expect(renderToStaticMarkup(<EndpointTestPanel error={{ status: 500 }} loading={false} />)).toContain("boom");
		expect(renderToStaticMarkup(<EndpointTestPanel loading />)).toContain("Sending a sample task");
	});
});

describe("mapping fields", () => {
	const mapped: AgentTemplate = {
		...http,
		params: [
			{ name: "url", label: "URL", required: true },
			{ name: "auth_type", label: "Auth", required: false, kind: "select", options: ["none", "oauth2"], option_labels: { oauth2: "OAuth 2 client credentials" }, group: "auth" },
			{ name: "token_url", label: "Token URL", required: false, group: "auth", show_when: { auth_type: ["oauth2"] } },
			{ name: "client_secret", label: "Client secret", required: false, kind: "secret", group: "auth", show_when: { auth_type: ["oauth2"] } },
			{ name: "answer_path", label: "Answer path", required: false, group: "mapping" },
		],
	};

	it("shows a field only for its listed values", () => {
		const tok = mapped.params[2];
		expect(paramVisible(tok, { auth_type: "none" })).toBe(false);
		expect(paramVisible(tok, {})).toBe(false);
		expect(paramVisible(tok, { auth_type: "oauth2" })).toBe(true);
		expect(paramVisible(mapped.params[0], {})).toBe(true);
	});

	it("labels select options", () => {
		expect(optionLabel(mapped.params[1], "oauth2")).toBe("OAuth 2 client credentials");
		expect(optionLabel(mapped.params[1], "none")).toBe("none");
		expect(optionLabel({ name: "framework", label: "F", required: false }, "crewai")).toBe("CrewAI");
	});

	it("renders the mapping fold and hides fields until chosen", () => {
		const hidden = renderToStaticMarkup(<ParamFields tpl={mapped} params={{ auth_type: "none" }} onChange={() => undefined} />);
		expect(hidden).toContain('data-testid="param-group-mapping"');
		expect(hidden).toContain("Request and answer mapping");
		expect(hidden).toContain("OAuth 2 client credentials");
		expect(hidden).not.toContain("param-token_url");
		const shown = renderToStaticMarkup(<ParamFields tpl={mapped} params={{ auth_type: "oauth2" }} onChange={() => undefined} />);
		expect(shown).toContain("param-token_url");
		expect(shown).toContain('data-testid="new-secret-client_secret"');
	});

	it("offers a new secret to admins only", () => {
		role.admin = false;
		const html = renderToStaticMarkup(<ParamFields tpl={mapped} params={{ auth_type: "oauth2" }} onChange={() => undefined} />);
		role.admin = true;
		expect(html).toContain("param-client_secret");
		expect(html).not.toContain("new-secret-client_secret");
	});
});

describe("request preview and test details", () => {
	const sent = { step: "request", method: "POST", url: "https://a/run", headers: [["Authorization", "Bearer ****"]] as [string, string][], body: '{"q":"hi"}' };

	it("shows the request and where the answer is read", () => {
		const html = renderToStaticMarkup(<EndpointPreviewView data={{ ok: true, requests: [sent], reads: "the JSON path output.text" }} loading={false} />);
		expect(html).toContain("POST");
		expect(html).toContain("https://a/run");
		expect(html).toContain("Authorization: Bearer ****");
		expect(html).toContain("{&quot;q&quot;:&quot;hi&quot;}");
		expect(html).toContain("Answer read from: the JSON path output.text");
	});

	it("shows why the preview is empty", () => {
		const html = renderToStaticMarkup(<EndpointPreviewView data={{ ok: false, error: "url is required", requests: [], reads: "" }} loading={false} />);
		expect(html).toContain("url is required");
		expect(renderToStaticMarkup(<EndpointPreviewView loading={false} />)).toContain("Fill in the required fields");
	});

	it("shows requests sent, raw response and reads after a test", () => {
		const res: EndpointTestResult = { ok: true, matched: true, adapter: "remote_http", url: "https://a/run", latency_ms: 5, answer: "READY", trace: [], notes: [], requests: [sent], raw_response: '{"output":{"text":"READY"}}', reads: "output.text" };
		const html = renderToStaticMarkup(<EndpointTestPanel result={res} loading={false} />);
		expect(html).toContain('data-testid="endpoint-test-requests"');
		expect(html).toContain("Requests sent (1)");
		expect(html).toContain('data-testid="endpoint-test-raw"');
		expect(html).toContain("Answer read from: output.text");
	});

	it("caps a long raw answer", () => {
		expect(capText("x".repeat(RAW_CAP + 10)).length).toBe(RAW_CAP + 4);
		expect(capText("short")).toBe("short");

describe("pull mode", () => {
	it("adds the pull option once and hides the model picker", () => {
		expect(withPullTemplate([http]).map((x) => x.id)).toEqual(["http-url", "pull"]);
		expect(withPullTemplate([http, PULL_TEMPLATE])).toHaveLength(2);
		expect(picksOwnModel(PULL_TEMPLATE, {})).toBe(true);
		expect(testable(PULL_TEMPLATE)).toBe(false);
	});

	it("writes the pull YAML", () => {
		expect(pullAgentYAML("bot", DEFAULT_PULL_OPTIONS)).toBe(
			"id: bot\nadapter: pull\nimage: bench/coding-core-base:1\nbudgets:\n  wall_s: 900\n  cpus: 1\n  mem_gb: 2\npull:\n  name: bot\n",
		);
		const y = pullAgentYAML("bot", { benchDrives: true, history: "server", repo: true }, "line one");
		expect(y.startsWith("# line one\n")).toBe(true);
		expect(y).toContain("pull:\n  name: bot\n  repo: tarball\n  conversation:\n    driver: bench\n    history: server\n");
	});

	it("renders the options, the history choice only when Heimdall drives", () => {
		const off = renderToStaticMarkup(<PullOptionsFields value={DEFAULT_PULL_OPTIONS} onChange={() => {}} />);
		expect(off).toContain("Heimdall drives the chat");
		expect(off).toContain("Coding tasks: send the repository, take a patch back");
		expect(off).not.toContain("pull-history");
		const on = renderToStaticMarkup(<PullOptionsFields value={{ ...DEFAULT_PULL_OPTIONS, benchDrives: true }} onChange={() => {}} />);
		expect(on).toContain("Send the whole chat each turn");
		expect(on).toContain("Agent keeps its own history");
	});
});
