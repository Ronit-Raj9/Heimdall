import "@/lib/i18n";

import { errorQuery, mutation, okQuery, render } from "@/lib/testing/render";
import type { GwCardLine, GwReportCard } from "@/lib/types/gateway";
import { describe, expect, it, vi } from "vitest";
import GatewayView, { GatewayCardLineRow, GatewayHeaderLine } from "./gatewayView";

const state = vi.hoisted(() => ({ card: undefined as unknown }));

vi.mock("@tanstack/react-router", () => ({
	Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
	useNavigate: () => () => undefined,
}));
vi.mock("@/lib/store/apis/gatewayApi", () => ({
	useGetGatewayReportCardQuery: () => state.card,
	useSetGatewaySourceMutation: () => mutation(),
	useRemoveGatewaySourceMutation: () => mutation(),
	useGetGatewayAgentsQuery: () => ({ data: undefined }),
	useImportGatewayAgentMutation: () => mutation(),
	useCreateAgentFromYamlMutation: () => mutation(),
	useGetModelWatchQuery: () => ({ data: undefined }),
	useRetryModelWatchMutation: () => mutation(),
	useDraftGatewayTestMutation: () => mutation(),
}));
vi.mock("@/lib/store/apis/schedulesApi", async (importOriginal) => ({
	...(await importOriginal<typeof import("@/lib/store/apis/schedulesApi")>()),
	useGetSchedulesQuery: () => okQuery({ rows: [] }),
}));
vi.mock("@/lib/store/apis/secretsApi", () => ({ useListSecretsQuery: () => okQuery({ secrets: [{ id: "s1", name: "BIFROST_ADMIN" }] }) }));

const lines: GwCardLine[] = [
	{
		feature: "semantic_cache",
		name: "Semantic cache",
		enabled: "on",
		enabled_source: "the gateway's loaded plugins",
		state: "measured",
		result: "saved",
		headline: "The cache saved 31% of cost.",
		source: { kind: "runs", runs: 160, rows: 900, raw_rows: 900, text: "160 runs of comparison e1" },
		exp_id: "e1",
		measured_at: "2026-11-20T10:00:00Z",
		start: "semantic_cache",
	},
	{
		feature: "providers",
		name: "Same model on several providers",
		enabled: "unknown",
		enabled_source: "Heimdall cannot see your production routing",
		state: "unknown",
		headline: "Not measured yet",
	},
	{
		feature: "batch",
		name: "Batch API for judge calls",
		enabled: "off",
		enabled_source: "Heimdall",
		state: "not_measured",
		headline: "Heimdall does not use Bifrost's batch API yet",
	},
	{
		feature: "complexity_router",
		name: "Complexity router",
		enabled: "on",
		enabled_source: "the gateway",
		state: "not_enough_data",
		headline: "",
	},
];

const card: GwReportCard = {
	gateway: { configured: true, host: "bifrost:8080", version: "v2.5.1", reachable: true, plugins: ["governance"] },
	source: { connected: true, host: "bifrost.example.com", mode: "saved", error: "" },
	lines,
	checked_at: "2026-11-27T08:00:00Z",
};

describe("Gateway report card", () => {
	it("renders the header, the source and every line", () => {
		state.card = okQuery(card);
		const html = render(<GatewayView />);
		expect(html).toContain("Gateway report card");
		expect(html).toContain("Bifrost v2.5.1 at bifrost:8080");
		expect(html).toContain("Your Bifrost: connected to bifrost.example.com");
		for (const l of lines) expect(html).toContain(`data-testid="gateway-line-${l.feature}"`);
		expect(html).toContain("Watch for new models");
	});

	it("renders each state: measured, unknown with its reason, not measured, not enough data", () => {
		const row = (l: GwCardLine) => render(<GatewayCardLineRow line={l} onStart={() => undefined} />);
		const measured = row(lines[0]);
		expect(measured).toContain("In use: On");
		expect(measured).toContain("Saved");
		expect(measured).toContain("From: 160 runs of comparison e1, 2026-11-20");
		expect(measured).toContain("Open the test");
		expect(measured).toContain("Start a test");
		const unknown = row(lines[1]);
		expect(unknown).toContain("In use: Unknown");
		expect(unknown).toContain("Unknown: Heimdall cannot see your production routing");
		expect(unknown).not.toContain("Start a test");
		expect(row(lines[2])).toContain("Heimdall does not use Bifrost&#x27;s batch API yet");
		expect(row(lines[3])).toContain("Not enough data yet");
	});

	it("says why the gateway could not be reached", () => {
		const html = render(
			<GatewayHeaderLine gateway={{ configured: true, reachable: false, error: "the gateway did not answer", plugins: [] }} />,
		);
		expect(html).toContain("Heimdall could not reach your gateway: the gateway did not answer");
	});

	it("shows the connect form to an admin when no Bifrost is connected", () => {
		state.card = okQuery({ ...card, source: { connected: false, host: "", mode: "none", error: "" } });
		const html = render(<GatewayView />);
		expect(html).toContain("Connect your Bifrost");
		expect(html).toContain("Bifrost address");
		expect(html).toContain("Secret that holds the admin credential");
		expect(html).toContain("BIFROST_ADMIN");
	});

	it("shows the load error", () => {
		state.card = errorQuery(500, "boom");
		expect(render(<GatewayView />)).toContain("Could not load the gateway report card");
	});
});