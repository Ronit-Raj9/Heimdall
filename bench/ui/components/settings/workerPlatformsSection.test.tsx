import "@/lib/i18n";

import type { WorkerPlatform } from "@/lib/store/apis/workerPlatformsApi";
import { platformStatusTone } from "@/lib/store/apis/workerPlatformsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PlatformCard, connectHint, platformsOff } from "./workerPlatformsSection";

const noop = () => {};
const base: WorkerPlatform = {
	name: "modal",
	title: "Modal",
	token_hint: "A Modal API token, as token-id:token-secret.",
	endpoint_label: "Environment (optional, the Modal environment name)",
	docs_url: "https://modal.com/docs/reference/modal.config",
	connected: false,
	default: false,
};

const card = (p: WorkerPlatform, extra: Partial<Parameters<typeof PlatformCard>[0]> = {}) =>
	renderToStaticMarkup(<PlatformCard platform={p} onConnect={noop} onTest={noop} onDefault={noop} onDisconnect={noop} {...extra} />);

describe("PlatformCard", () => {
	it("asks for a token when not connected, as a password field", () => {
		const html = card(base);
		expect(html).toContain('data-testid="platform-modal-form"');
		expect(html).toContain('type="password"');
		expect(html).toContain("Test and connect");
		expect(html).toContain("Not connected");
		expect(html).toContain("Environment (optional");
		expect(html).not.toContain("platform-modal-disconnect");
	});

	it("shows a connected default platform with its last four characters only", () => {
		const html = card({
			...base,
			connected: true,
			default: true,
			token_last4: "9f2a",
			status: "ok",
			last_tested_at: "2026-11-20T00:00:00Z",
		});
		expect(html).toContain("Token ending 9f2a");
		expect(html).toContain('data-testid="platform-modal-default"');
		expect(html).toContain("Working");
		expect(html).toContain('data-testid="platform-modal-test"');
		expect(html).toContain('data-testid="platform-modal-disconnect"');
		expect(html).not.toContain("platform-modal-make-default");
		expect(html).not.toContain("platform-modal-form");
	});

	it("offers default on a connected platform and shows a refused token", () => {
		const html = card({ ...base, connected: true, status: "invalid" }, { error: "the platform refused the token" });
		expect(html).toContain('data-testid="platform-modal-make-default"');
		expect(html).toContain("Token refused");
		expect(html).toContain("Could not update Modal: the platform refused the token");
	});

	it("uses plain words, no em dashes", () => {
		const html = card({
			...base,
			connected: true,
			default: true,
			token_last4: "abcd",
			status: "error",
		});
		expect(html).not.toMatch(/[–—]/);
		expect(html).not.toMatch(/\b(runner|lease|sandbox driver|probe)\b/i);
	});
});

describe("helpers", () => {
	it("maps status to tone and detects platforms off", () => {
		expect(platformStatusTone("ok")).toBe("success");
		expect(platformStatusTone("invalid")).toBe("warning");
		expect(platformStatusTone(undefined)).toBe("outline");
		expect(platformsOff({ status: 503 })).toBe(true);
		expect(platformsOff({ status: 500 })).toBe(false);
	});
});
describe("why Test and connect is disabled (N14)", () => {
	it("says what to paste while the button is disabled", () => {
		const html = card(base);
		expect(html).toContain("Paste the token to test it.");
		expect(html).toContain('aria-describedby="');
		const needsEndpoint = card({ ...base, name: "daytona", title: "Daytona", endpoint_label: "API URL", endpoint_required: true });
		expect(needsEndpoint).toContain("Paste the token and the endpoint to test them.");
	});

	it("drops the hint once the fields are filled", () => {
		expect(connectHint("", "", false)).toBe("needToken");
		expect(connectHint("", "", true)).toBe("needTokenAndEndpoint");
		expect(connectHint("tok", "", true)).toBe("needTokenAndEndpoint");
		expect(connectHint("tok", "", false)).toBeUndefined();
		expect(connectHint("tok", "https://x", true)).toBeUndefined();
	});
});
