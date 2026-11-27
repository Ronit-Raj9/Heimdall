import i18n from "@/lib/i18n";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { eventsFor } from "./integrationsView";

// The hook form offers every event the server's webhooks.Events lists (wave 4 adds the two improvement
// events, I42, and result.changed, I36), and a PagerDuty hook the paging ones (webhooks/format.go).
const serverEvents = () => {
	const src = ["webhooks.go", "resultchanged_w449.go", "monitorevents.go"]
		.map((f) => readFileSync(new URL(`../../../../webhooks/${f}`, import.meta.url), "utf8"))
		.join("\n");
	return [...src.matchAll(/Event[A-Za-z]+ += "([a-z]+\.[a-z]+)"/g)].map((m) => m[1]).filter((e) => e !== "ping");
};

describe("webhook events", () => {
	it("offers every event the server accepts, each with a label", () => {
		const offered = eventsFor("json");
		for (const e of serverEvents()) {
			expect(offered, e).toContain(e);
			expect(i18n.t(`webhooks.eventNames.${e}`, { ns: "integrations" }), e).not.toContain("eventNames");
		}
	});
	it("lets a PagerDuty hook page on a paused improvement run", () => {
		expect(eventsFor("pagerduty")).toContain("improvement.paused");
		expect(eventsFor("pagerduty")).not.toContain("improvement.finished");
	});
});