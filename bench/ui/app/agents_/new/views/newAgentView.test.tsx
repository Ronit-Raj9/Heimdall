import "@/lib/i18n";

import type { AgentTemplate } from "@/lib/types/agentBuilder";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import NewAgentView, { defaultAgentName } from "./newAgentView";

const templates: AgentTemplate[] = [
	{ id: "codex", name: "Codex CLI", category: "cli", description: "Codex", adapter: "command", providers: ["openai"], default_model: "gpt-x", builds: true, params: [] },
	{
		id: "http-url",
		name: "Your HTTP endpoint",
		category: "bring-your-own",
		description: "HTTP",
		adapter: "remote_http",
		providers: ["openai", "anthropic"],
		default_model: "m",
		builds: false,
		params: [{ name: "url", label: "Endpoint URL", required: true }],
	},
];

vi.mock("@/lib/store/apis/agentBuilderApi", () => ({
	useGetAgentTemplatesQuery: () => ({ isLoading: false, isError: false, data: { templates }, refetch: () => undefined }),
	useCreateAgentFromTemplateMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/benchApi", () => ({ getBenchErrorMessage: () => "err" }));
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
		expect(html).toContain("Coding CLIs");
		expect(html).toContain("Bring your own");
		expect(html).toContain("Pick a template to continue.");
	});

	it("shows the form for the selected template with its params", () => {
		const html = render("?template=http-url");
		expect(html).toContain('aria-pressed="true"');
		expect(html).toContain('data-testid="agent-create"');
		expect(html).toContain('data-testid="param-url"');
	});

	it("pre-fills a valid name", () => {
		expect(defaultAgentName(templates[0])).toBe("my-codex");
	});
});
