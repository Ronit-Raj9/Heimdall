import "@/lib/i18n";

import type { AgentTemplate } from "@/lib/types/agentBuilder";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import NewAgentView, { defaultAgentName, nameProblem } from "./newAgentView";

const templates: AgentTemplate[] = [
	{
		id: "codex",
		name: "Codex CLI",
		category: "cli",
		description: "Codex",
		adapter: "command",
		providers: ["openai"],
		default_model: "gpt-x",
		builds: true,
		params: [],
	},
	{
		id: "plain-loop",
		name: "Plain loop",
		category: "framework",
		description: "Loop",
		adapter: "command",
		providers: ["openai"],
		default_model: "gpt-x",
		builds: true,
		params: [],
	},
	{
		id: "http-url",
		name: "Your HTTP endpoint",
		category: "bring-your-own",
		description: "HTTP",
		adapter: "remote_http",
		providers: ["openai", "anthropic"],
		default_model: "m",
		builds: false,
		params: [
			{ name: "url", label: "Endpoint URL", required: true },
			{ name: "auth_secret", label: "Auth token", required: false, kind: "secret", group: "auth" },
			{ name: "tools", label: "Let my agent call the task's tools", required: false, kind: "bool", default: "false", group: "tools" },
			{ name: "model_traffic", label: "Route", required: false, kind: "bool", default: "false", group: "advanced" },
		],
	},
	{
		id: "wrap-app",
		name: "Wrap my existing app",
		category: "bring-your-own",
		description: "Wrap",
		adapter: "command",
		providers: ["openai"],
		default_model: "m",
		builds: false,
		params: [
			{
				name: "framework",
				label: "Framework",
				required: false,
				kind: "select",
				options: ["langgraph", "crewai", "openai-agents", "custom"],
				default: "custom",
			},
			{ name: "repo_url", label: "Git repository", required: false },
			{ name: "image", label: "Docker image", required: false },
			{ name: "command", label: "Start command", required: true },
		],
	},
];

vi.mock("@/lib/store/apis/agentBuilderApi", () => ({
	useGetAgentTemplatesQuery: () => ({ isLoading: false, isError: false, data: { templates }, refetch: () => undefined }),
	useCreateAgentFromTemplateMutation: () => [vi.fn(), { isLoading: false }],
	useTestEndpointMutation: () => [vi.fn(), { isLoading: false, reset: () => undefined }],
	usePreviewEndpointMutation: () => [vi.fn(), { isLoading: false }],
	usePutAgentConfigMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/secretsApi", () => ({ useCreateSecretMutation: () => [vi.fn(), { isLoading: false }] }));
let agentIds = ["taken"];
vi.mock("@/lib/store/apis/benchApi", () => ({
	getBenchErrorMessage: () => "err",
	useGetAgentsQuery: () => ({ data: { agents: agentIds.map((id) => ({ id })) } }),
}));
vi.mock("./platformImport", () => ({ PlatformImportCard: () => <div data-testid="platform-import" /> }));
vi.mock("@/lib/session/sessionContext", () => ({ canUse: () => true, useSession: () => ({}) }));
vi.mock("@/components/models/modelPicker", () => ({ ModelPicker: () => <div data-testid="model-picker" /> }));
vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => vi.fn(),
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}));

const render = (search = "") =>
	renderToStaticMarkup(
		<NuqsTestingAdapter searchParams={search}>
			<NewAgentView />
		</NuqsTestingAdapter>,
	);

describe("NewAgentView", () => {
	it("groups templates by category and asks to pick one", () => {
		const html = render();
		expect(html).toContain('data-testid="template-codex"');
		expect(html).toContain('data-testid="template-http-url"');
		expect(html).toContain("Coding command-line tools");
		expect(html).toContain("Bring your own");
		expect(html).toContain("Pick a template to continue.");
		expect(html).toContain('data-testid="platform-import"');
	});

	it("shows the form for the selected template with its params", () => {
		const html = render("?template=http-url");
		expect(html).toContain('aria-pressed="true"');
		expect(html).toContain('data-testid="agent-create"');
		expect(html).toContain('data-testid="param-url"');
		expect(html).toContain('data-testid="param-group-auth"');
		expect(html).toContain('data-testid="param-group-tools"');
		expect(html).toContain('data-testid="endpoint-test-button"');
		// An HTTP endpoint picks its own model unless its model calls go through Heimdall.
		expect(html).toContain('data-testid="agent-own-model"');
		expect(html).not.toContain('data-testid="model-picker"');
	});

	it("wraps an existing app with a framework hint and no endpoint test", () => {
		const html = render("?template=wrap-app");
		expect(html).toContain('data-testid="param-framework"');
		expect(html).toContain('data-testid="framework-hint"');
		expect(html).toContain('data-testid="model-picker"');
		expect(html).not.toContain('data-testid="endpoint-test-button"');
	});

	it("offers pull mode with its options and no model picker", () => {
		expect(render()).toContain('data-testid="template-pull"');
		const html = render("?template=pull");
		expect(html).toContain('data-testid="pull-fields"');
		expect(html).toContain('data-testid="agent-own-model"');
		expect(html).not.toContain('data-testid="endpoint-test"');
		expect(html).toContain("Runs in your code");
	});

	it("pre-fills a valid name", () => {
		expect(defaultAgentName(templates[0])).toBe("my-codex");
	});

	it("pre-fills the first free name, skipping agents that already exist", () => {
		expect(defaultAgentName(templates[0], new Set(["my-codex"]))).toBe("my-codex-2");
		expect(defaultAgentName(templates[0], new Set(["my-codex", "my-codex-2"]))).toBe("my-codex-3");
		expect(defaultAgentName(templates[0], new Set(["other"]))).toBe("my-codex");
		agentIds = ["my-plain-loop"];
		const html = render("?template=plain-loop");
		expect(html).toContain('value="my-plain-loop-2"');
		expect(html).not.toContain('value="my-plain-loop"');
		expect(html).not.toContain("already exists");
		agentIds = ["taken"];
	});

	it("blocks Create for a taken name, for every template", () => {
		expect(nameProblem("my-plain-loop", new Set(["my-plain-loop"]))).toBe(
			"An agent named my-plain-loop already exists. Pick another name.",
		);
		expect(nameProblem("my-plain-loop-2", new Set(["my-plain-loop"]))).toBe("");
		expect(nameProblem("bad Name", new Set())).toBe("Use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit.");
	});

	it("names each name problem in order, the 63 limit with the count", () => {
		expect(nameProblem("", new Set())).toBe("Give the agent a name.");
		expect(nameProblem("a".repeat(120), new Set())).toBe("Use at most 63 characters (120 now).");
		expect(nameProblem("-x", new Set())).toBe("Start with a lowercase letter or a digit.");
		expect(nameProblem("Upper", new Set())).toBe("Start with a lowercase letter or a digit.");
		expect(nameProblem("a b", new Set())).toBe("Use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit.");
		expect(nameProblem("a".repeat(63), new Set())).toBe("");
	});

	it("says the limit and the real file the name is saved as", () => {
		agentIds = [];
		const html = render("?template=plain-loop");
		expect(html).toContain("up to 63 characters. Saved as agents/my-plain-loop.yaml.");
		agentIds = ["taken"];
	});
});