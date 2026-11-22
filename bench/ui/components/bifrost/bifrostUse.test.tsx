import "@/lib/i18n";

import type { BifrostUse } from "@/lib/types/bifrostUse";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BifrostFeatureList, BifrostGatewayLine, bifrostUseStatus } from "./bifrostUse";

const use: BifrostUse = {
	gateway_configured: true,
	gateway_host: "bifrost:8080",
	reachable: true,
	loaded_plugins: ["governance", "heimdall", "logging"],
	features: [
		{ id: "virtual_keys", name: "Virtual key per run", state: "on", detail: "One key per run.", docs: "https://docs.getbifrost.ai/features/governance/virtual-keys", code: "bench/gateways/bifrost/keys.go" },
		{ id: "semantic_cache", name: "Semantic cache", state: "available", detail: "Cache on vs off.", docs: "https://docs.getbifrost.ai/features/semantic-caching" },
	],
};

describe("Bifrost use", () => {
	it("lists features with state, docs link and code path", () => {
		const html = renderToStaticMarkup(<BifrostFeatureList features={use.features} testId="f" />);
		expect(html).toContain("Virtual key per run");
		expect(html).toContain("In use");
		expect(html).toContain("Available");
		expect(html).toContain("https://docs.getbifrost.ai/features/governance/virtual-keys");
		expect(html).toContain("bench/gateways/bifrost/keys.go");
		expect(html).toContain('data-state="available"');
	});
	it("shows the gateway and loaded plugins, or why it could not be asked", () => {
		expect(renderToStaticMarkup(<BifrostGatewayLine use={use} />)).toContain("governance, heimdall, logging");
		const down = renderToStaticMarkup(<BifrostGatewayLine use={{ ...use, reachable: false, check_error: "gateway not reachable" }} />);
		expect(down).toContain("gateway not reachable");
		expect(renderToStaticMarkup(<BifrostGatewayLine use={{ ...use, gateway_configured: false }} />)).toContain("No Bifrost gateway");
	});
	it("counts features in use for the card status", () => {
		expect(bifrostUseStatus(use)).toEqual({ tone: "ok", count: 1 });
		expect(bifrostUseStatus({ ...use, reachable: false })).toEqual({ tone: "warn", count: 1 });
		expect(bifrostUseStatus(undefined)).toEqual({ tone: "idle", count: 0 });
	});
});
