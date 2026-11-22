import "@/lib/i18n";

import type { AgentPlatform } from "@/lib/types/agentImport";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { ConnectPlatformForm } from "./connectView";

vi.mock("@/lib/store/apis/agentImportApi", () => ({
	useGetAgentPlatformsQuery: () => ({ isError: false, data: { platforms: [] } }),
	useImportAgentFromPlatformMutation: () => [vi.fn(), { isLoading: false }],
}));
vi.mock("@/lib/store/apis/secretsApi", () => ({ useListSecretsQuery: () => ({ data: { secrets: [{ name: "VAPI_PRIVATE_KEY" }] } }) }));
vi.mock("@/lib/session/sessionContext", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/session/sessionContext")>()),
	canUse: () => true,
	useSession: () => ({}),
}));
vi.mock("@tanstack/react-router", async () => (await import("@/lib/testing/routerMock")).routerMock);

const vapi: AgentPlatform = {
	id: "vapi",
	label: "Vapi",
	kind: "voice",
	help: "A Vapi assistant.",
	id_label: "Assistant id",
	id_placeholder: "e.g. 5f1c-assistant",
	inputs: [{ name: "phone_number_id", label: "Phone number id", placeholder: "pn-1", help: "Only for phone calls." }],
	secrets: [{ role: "api_key", label: "Private API key", required: true, suggested: "VAPI_PRIVATE_KEY" }],
	imports: ["prompt", "voice"],
	docs: "https://docs.vapi.ai",
};

// B6: a platform picked on Connect shows that platform's own fields.
describe("connect platform form", () => {
	it("renders the platform's id, inputs, secret names, help and docs", () => {
		const html = renderToStaticMarkup(<ConnectPlatformForm platform={vapi} />);
		expect(html).toContain('data-testid="connect-platform-form"');
		expect(html).toContain("Assistant id");
		expect(html).toContain('placeholder="e.g. 5f1c-assistant"');
		expect(html).toContain('data-testid="platform-input-phone_number_id"');
		expect(html).toContain("Only for phone calls.");
		expect(html).toContain('data-testid="platform-secret-api_key"');
		expect(html).toContain('value="VAPI_PRIVATE_KEY"');
		expect(html).toContain("A Vapi assistant.");
		expect(html).toContain('href="https://docs.vapi.ai"');
		expect(html).toContain('<option value="VAPI_PRIVATE_KEY">');
		expect(html).toContain('data-testid="connect-platform-read"');
	});
});