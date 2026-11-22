import "@/lib/i18n";

import type { AgentImportResponse, AgentPlatform } from "@/lib/types/agentImport";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ImportFound, importedAgentName, importRequest, missingImportFields, PlatformImportCard } from "./platformImport";

vi.mock("@/lib/store/apis/agentImportApi", () => ({
	useGetAgentPlatformsQuery: () => ({ isError: false, data: { platforms: [] } }),
	useImportAgentFromPlatformMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/secretsApi", () => ({ useListSecretsQuery: () => ({ data: { secrets: [] } }) }));
vi.mock("@/lib/store/apis/benchApi", () => ({ getBenchErrorMessage: () => "err" }));
vi.mock("@/lib/session/sessionContext", () => ({ canUse: () => true, useSession: () => ({}) }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));

const agentcore: AgentPlatform = {
	id: "agentcore",
	label: "AWS Bedrock AgentCore",
	kind: "endpoint",
	help: "An AgentCore Runtime agent.",
	id_label: "Runtime ARN or id",
	id_placeholder: "arn:aws:bedrock-agentcore:...",
	inputs: [
		{ name: "region", label: "Region" },
		{ name: "qualifier", label: "Endpoint qualifier" },
	],
	secrets: [
		{ role: "access_key", label: "AWS access key id", required: true, suggested: "AGENTCORE_ACCESS_KEY_ID" },
		{ role: "secret_key", label: "AWS secret access key", required: true, suggested: "AGENTCORE_SECRET_ACCESS_KEY" },
		{ role: "session_token", label: "AWS session token", suggested: "AGENTCORE_SESSION_TOKEN" },
	],
	imports: ["name", "protocol"],
	docs: "https://docs.aws.amazon.com",
};

describe("platform import", () => {
	it("names the agent after the platform and the agent", () => {
		expect(importedAgentName("vapi", "Support Line #2")).toBe("vapi-support-line-2");
		expect(importedAgentName("copilot_studio", "")).toBe("copilot-studio-agent");
		expect(importedAgentName("agentcore", "arn:aws:bedrock-agentcore:us-east-1:1:runtime/x-AbCdEf1234")).toMatch(/^[a-z0-9][a-z0-9._-]{0,62}$/);
	});

	it("builds the request without empty fields", () => {
		const req = importRequest(agentcore, "core", " rt-1 ", { region: "us-east-1", qualifier: " " }, { access_key: "AK", secret_key: "SK", session_token: "" }, true);
		expect(req).toEqual({
			id: "core",
			platform: "agentcore",
			agent_id: "rt-1",
			inputs: { region: "us-east-1" },
			secrets: { access_key: "AK", secret_key: "SK" },
			dry_run: true,
		});
		expect(importRequest(agentcore, "core", "rt-1", {}, {}, false).dry_run).toBeUndefined();
	});

	it("lists what is missing", () => {
		expect(missingImportFields(agentcore, "", {}, { access_key: "AK" })).toEqual(["Runtime ARN or id", "AWS secret access key"]);
		expect(missingImportFields(agentcore, "rt", {}, { access_key: "AK", secret_key: "SK" })).toEqual([]);
	});

	it("renders closed by default", () => {
		const html = renderToStaticMarkup(<PlatformImportCard />);
		expect(html).toContain("Import from a platform");
		expect(html).toContain('data-testid="platform-import-toggle"');
		expect(html).not.toContain('data-testid="platform-import-form"');
	});

	it("shows what the platform said, warnings and the config", () => {
		const res: AgentImportResponse = {
			id: "vapi-support",
			path: "agents/vapi-support.yaml",
			files: ["agents/vapi-support.yaml", "agents/imported/vapi-support.json"],
			yaml: "id: vapi-support\nadapter: voice_platform\n",
			dry_run: true,
			validation_error: "schema: voice_platform: unknown field",
			import: {
				platform: "vapi",
				agent_id: "asst-1",
				adapter: "voice_platform",
				block: {},
				capability_hints: ["voice", "tools"],
				warnings: ["Only the first 20 saved tools were read."],
				fetched_at: "2026-11-22T10:00:00Z",
				definition: {
					name: "Support line",
					first_message: "Hello.",
					prompt: "Be kind.",
					model: "openai/gpt-4.1",
					tools: [{ name: "lookup_order" }],
					voice: { provider: "11labs", voice_id: "rachel" },
					source: "https://api.vapi.ai/assistant/asst-1",
				},
			},
		};
		const html = renderToStaticMarkup(<ImportFound res={res} platform={{ ...agentcore, id: "vapi", label: "Vapi", kind: "voice" }} />);
		expect(html).toContain("Found on Vapi");
		expect(html).toContain("Support line");
		expect(html).toContain("11labs / rachel");
		expect(html).toContain("lookup_order");
		expect(html).toContain('data-testid="platform-import-warnings"');
		expect(html).toContain('data-testid="platform-import-invalid"');
		expect(html).toContain("adapter: voice_platform");
	});
});
