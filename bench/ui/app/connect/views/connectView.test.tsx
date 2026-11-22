import "@/lib/i18n";

import type { AgentPlatform } from "@/lib/types/agentImport";
import type { ConnectCandidate, ConnectDetectResponse } from "@/lib/types/connect";
import i18n from "i18next";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { canSave, resultTitle, showDetectButton } from "./connect.utils";
import { CandidateStatus, ConnectPlatformForm, PlatformCatalogNote, UnsupportedCandidates } from "./connectView";
import { MappingPreview } from "./mappingPreview";

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
const tc = i18n.getFixedT(null, "connect") as unknown as (key: string, vars?: Record<string, string>) => string;
const titleOf = (det: ConnectDetectResponse, cand: ConnectCandidate | undefined) => {
	const r = resultTitle(det, cand);
	return tc(r.key, r.vars);
};

const httpCand = (over: Partial<ConnectCandidate> = {}): ConnectCandidate => ({
	kind: "http",
	preset: "custom",
	label: "HTTP endpoint",
	confidence: 0.4,
	evidence: [],
	supported: true,
	template: "http-url",
	params: { url: "https://example.com/nope" },
	nonce_found: false,
	...over,
});

const detOf = (over: Partial<ConnectDetectResponse>): ConnectDetectResponse =>
	({ class: "url", candidates: [], probes: [], from: "the shared worker", ...over }) as ConnectDetectResponse;

// N105, N106: the result says what is known, and Save needs a proven answer.
describe("connect result", () => {
	it("unknown input shows Not recognised and no Unknown label", () => {
		const det = detOf({
			class: "unknown",
			candidates: [{ ...httpCand(), kind: "", label: "Unknown", supported: false, message: "Paste an endpoint URL." }],
		});
		expect(titleOf(det, undefined)).toBe("Not recognised");
		const html = renderToStaticMarkup(<UnsupportedCandidates det={det} />);
		expect(html).toContain("Paste an endpoint URL.");
		expect(html).not.toContain("Unknown");
	});

	it("an endpoint that never answered says Could not connect", () => {
		const det = detOf({
			candidates: [httpCand({ supported: false, confidence: 0 })],
			failures: [{ code: "path", title: "Wrong path", message: "POST /nope answered 405" }],
		});
		expect(titleOf(det, undefined)).toBe("Could not connect: Wrong path");
	});

	it("an unproven candidate has Save disabled and no 40% sure", () => {
		const cand = httpCand();
		expect(titleOf(detOf({ candidates: [cand] }), cand)).toBe("Found: HTTP endpoint, not proven yet");
		const html = renderToStaticMarkup(<CandidateStatus cand={cand} />);
		expect(html).not.toContain("% sure");
		expect(html).toContain("The check code did not come back, so Heimdall does not know where the answer is yet.");
		expect(canSave(cand, false)).toBe(false);
		expect(canSave(cand, true)).toBe(true);
		expect(renderToStaticMarkup(<MappingPreview candidate={cand} />)).not.toContain("own request and answer shape");
	});

	it("a proven candidate enables Save and shows how sure", () => {
		const cand = httpCand({ proven: true, nonce_found: true, confidence: 0.9 });
		expect(titleOf(detOf({ candidates: [cand] }), cand)).toBe("Found: HTTP endpoint");
		const html = renderToStaticMarkup(<CandidateStatus cand={cand} />);
		expect(html).toContain("90% sure");
		expect(html).not.toContain("did not come back");
		expect(canSave(cand, false)).toBe(true);
		expect(renderToStaticMarkup(<MappingPreview candidate={cand} />)).toContain("own request and answer shape");
	});
});

// N107: no Detect button while the platform catalog loads; an error links to Import from a platform.
describe("connect platform catalog", () => {
	it("platform picked while catalog loads shows no Detect button", () => {
		expect(showDetectButton("vapi", undefined, { isLoading: true, isError: false })).toBe(false);
		expect(showDetectButton("none", undefined, { isLoading: true, isError: false })).toBe(true);
		expect(showDetectButton("vapi", vapi, { isLoading: false, isError: false })).toBe(false);
		expect(showDetectButton("langgraph", undefined, { isLoading: false, isError: false })).toBe(true);
		const html = renderToStaticMarkup(<PlatformCatalogNote label="Vapi" loading error={undefined} />);
		expect(html).toContain("Loading the Vapi form...");
	});

	it("a catalog error shows the error and a link to Import from a platform", () => {
		const html = renderToStaticMarkup(<PlatformCatalogNote label="Vapi" loading={false} error="server down" />);
		expect(html).toContain("server down");
		expect(html).toContain('href="/agents/new"');
		expect(html).toContain("Import from a platform");
	});
});