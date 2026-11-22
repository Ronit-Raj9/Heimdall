import "@/lib/i18n";

import type { AgentTemplate, EndpointTestResult } from "@/lib/types/agentBuilder";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { defaultParams, detectFramework, EndpointTestPanel, missingParams, picksOwnModel, testable } from "./connectForm";

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
