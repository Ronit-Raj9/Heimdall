import "@/lib/i18n";

import type { AgentTemplate, EndpointTestResult } from "@/lib/types/agentBuilder";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import {
	DEFAULT_PULL_OPTIONS,
	defaultParams,
	detectFramework,
	EndpointTestPanel,
	missingParams,
	picksOwnModel,
	pullAgentYAML,
	PULL_TEMPLATE,
	PullOptionsFields,
	testable,
	withPullTemplate,
} from "./connectForm";

vi.mock("@/lib/store/apis/benchApi", () => ({ getBenchErrorMessage: () => "boom" }));
vi.mock("@/lib/store/apis/agentBuilderApi", () => ({ useTestEndpointMutation: () => [vi.fn(), { isLoading: false }] }));

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
