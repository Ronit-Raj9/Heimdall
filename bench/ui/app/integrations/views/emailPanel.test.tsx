import "@/lib/i18n";

import type { EmailSettings } from "@/lib/types/integrations";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { EmailForm, sameChoice } from "./emailPanel";

const settings: EmailSettings = {
	available: true,
	delivers: false,
	provider: "log",
	email: "ana@example.com",
	events: ["evaluation.finished", "evaluation.regressed", "release.blocked", "schedule.failed", "worker.offline"],
	prefs: { events: ["evaluation.regressed"], digest: true },
};

const noop = () => {};

describe("email panel", () => {
	it("shows the caller's choice and says when the server only logs email", () => {
		const html = renderToStaticMarkup(
			<EmailForm settings={settings} events={["evaluation.regressed"]} digest dirty={false} onEvents={noop} onDigest={noop} onSave={noop} onTest={noop} />,
		);
		expect(html).toContain("ana@example.com");
		expect(html).toContain('data-testid="email-not-delivering"');
		expect(html).toContain("HEIMDALL_SMTP_URL");
		expect(html).toContain("Weekly digest");
		expect(html).toContain("An agent got worse");
	});

	it("hides the not-delivering note once the server sends mail", () => {
		const html = renderToStaticMarkup(
			<EmailForm settings={{ ...settings, delivers: true, provider: "smtp" }} events={[]} digest={false} dirty onEvents={noop} onDigest={noop} onSave={noop} onTest={noop} />,
		);
		expect(html).not.toContain("email-not-delivering");
	});

	it("explains why email is not available", () => {
		const html = renderToStaticMarkup(
			<EmailForm
				settings={{ ...settings, available: false, reason: "email settings belong to a signed-in member, not to an API token" }}
				events={[]}
				digest
				dirty={false}
				onEvents={noop}
				onDigest={noop}
				onSave={noop}
				onTest={noop}
			/>,
		);
		expect(html).toContain('data-testid="email-unavailable"');
		expect(html).toContain("not to an API token");
		expect(html).not.toContain("email-digest");
	});

	it("knows when the form differs from the saved choice", () => {
		expect(sameChoice(settings, ["evaluation.regressed"], true)).toBe(true);
		expect(sameChoice(settings, ["evaluation.regressed"], false)).toBe(false);
		expect(sameChoice(settings, ["evaluation.regressed", "worker.offline"], true)).toBe(false);
	});
});
