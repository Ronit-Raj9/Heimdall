import "@/lib/i18n";

import { inboxSourceParams } from "@/app/inbox/views/inboxConnect";
import type { PullCheck } from "@/lib/types/liveinbox";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PullSteps } from "./pullSteps";

const missing: PullCheck = {
	ready: false,
	configured: true,
	secrets: [{ name: "BF_TOKEN", for: "the gateway read", set: false }],
	steps: [
		{ problem: "the gateway read needs the secret BF_TOKEN", action: "Add an org secret named BF_TOKEN in Settings > Secrets", link: "/settings?tab=secrets", secret: "BF_TOKEN" },
		{ problem: "the gateway has no URL", action: "Set the URL in the setup form", link: "#live-setup" },
	],
};

describe("PullSteps", () => {
	it("links a missing secret to Settings > Secrets and a missing config to the form", () => {
		const html = renderToStaticMarkup(<PullSteps check={missing} testId="t" />);
		expect(html).toContain('href="/settings?tab=secrets"');
		expect(html).toContain('data-testid="pull-step-secrets"');
		expect(html).toContain('href="#live-setup"');
		expect(html).toContain("BF_TOKEN");
	});
	it("renders nothing once ready", () => {
		expect(renderToStaticMarkup(<PullSteps check={{ ...missing, ready: true, steps: [] }} testId="t" />)).toBe("");
	});
});

describe("inboxSourceParams", () => {
	it("needs the kind's required fields and trims", () => {
		expect(inboxSourceParams({ name: "gw", kind: "bifrost", url: "" })).toBeNull();
		expect(inboxSourceParams({ name: " gw ", kind: "bifrost", url: " https://gw.example.com ", token_env: "BF_TOKEN" })).toEqual({
			name: "gw",
			kind: "bifrost",
			url: "https://gw.example.com",
			token_env: "BF_TOKEN",
		});
		expect(inboxSourceParams({ name: "ls", kind: "langsmith", host: "https://api.smith.langchain.com", api_key_env: "K", project: "" })).toBeNull();
	});
});
