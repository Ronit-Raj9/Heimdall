import "@/lib/i18n";

import type { WorkerPlatform } from "@/lib/store/apis/workerPlatformsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PlatformRegistryForm, registryHint, takesRegistry } from "./platformRegistry";

const noop = () => {};
const modal: WorkerPlatform = { name: "modal", title: "Modal", token_hint: "", connected: true, default: false, token_last4: "9f2a" };
const form = (p: WorkerPlatform, extra: Partial<Parameters<typeof PlatformRegistryForm>[0]> = {}) =>
	renderToStaticMarkup(<PlatformRegistryForm platform={p} onSave={noop} onRemove={noop} {...extra} />);

describe("PlatformRegistryForm", () => {
	it("asks for a registry when none is saved, with the token as a password field", () => {
		const html = form(modal);
		expect(html).toContain("Modal needs an image registry it can pull from. Add one below.");
		// Q-247: one title only (the disclosure's); the block repeats no "Image registry" heading.
		expect(html).not.toContain(">Image registry<");
		expect(html).toContain('data-testid="platform-modal-registry-form"');
		expect(html).toContain('placeholder="ghcr.io/team/heimdall"');
		expect(html).toMatch(/type="password"[^>]*data-testid="platform-modal-registry-token"/);
		expect(html).toContain("Sign in and save");
		expect(html).toContain("Fill in the registry path, the user name and the token.");
	});

	it("shows a saved registry by path, user and last four characters only", () => {
		const html = form({
			...modal,
			registry: { registry: "ghcr.io/acme/heimdall", username: "acme-bot", token_last4: "c0de", tested_at: "2026-11-22T20:00:00Z" },
		});
		expect(html).toContain("ghcr.io/acme/heimdall as acme-bot");
		expect(html).toContain("token ending c0de");
		expect(html).not.toContain("registry-needed");
		expect(html).not.toContain('data-testid="platform-modal-registry-form"');
		expect(html).toContain('data-testid="platform-modal-registry-remove"');
	});

	it("shows the server's reason when the registry refused the login", () => {
		const html = form(modal, { error: "Heimdall could not sign in to ghcr.io/acme/heimdall: the user name or token was refused." });
		expect(html).toContain("Could not save the image registry: Heimdall could not sign in to ghcr.io/acme/heimdall");
	});
});

describe("registry helpers", () => {
	it("needs every field", () => {
		expect(registryHint("ghcr.io/a/b", "u", "")).toBe("needAll");
		expect(registryHint(" ", "u", "t")).toBe("needAll");
		expect(registryHint("ghcr.io/a/b", "u", "t")).toBeUndefined();
	});

	it("is shown only on a connected Modal card", () => {
		expect(takesRegistry(modal)).toBe(true);
		expect(takesRegistry({ ...modal, connected: false })).toBe(false);
		expect(takesRegistry({ ...modal, name: "e2b" })).toBe(false);
	});
});