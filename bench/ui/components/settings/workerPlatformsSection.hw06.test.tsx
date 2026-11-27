import "@/lib/i18n";

import type { WorkerPlatform } from "@/lib/store/apis/workerPlatformsApi";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PlatformCard } from "./workerPlatformsSection";

// No store here: the registry block renders its saved state without calls.
const noop = () => {};
const modal: WorkerPlatform = {
	name: "modal",
	title: "Modal",
	token_hint: "",
	connected: true,
	default: false,
	token_last4: "9f2a",
	status: "ok",
};
const card = (p: WorkerPlatform) =>
	renderToStaticMarkup(<PlatformCard platform={p} onConnect={noop} onTest={noop} onDefault={noop} onDisconnect={noop} />);

describe("PlatformCard (fix wave 3, N216)", () => {
	it("has the registry block and what runs on Modal once Modal is connected", () => {
		const html = card(modal);
		expect(html).toContain('data-testid="platform-modal-registry"');
		expect(html).toContain("Modal needs an image registry it can pull from. Add one below.");
		expect(html).toContain("Runs on Modal: repository tasks graded on the code change, with gateway faults.");
		expect(html).toContain("Not on Modal: some tasks cannot run in a cloud sandbox");
	});

	it("has neither before Modal is connected, nor on another platform", () => {
		expect(card({ ...modal, connected: false })).not.toContain("platform-modal-registry");
		const e2b = card({ ...modal, name: "e2b", title: "E2B" });
		expect(e2b).not.toContain("registry");
		expect(e2b).not.toContain("Runs on Modal");
	});
});