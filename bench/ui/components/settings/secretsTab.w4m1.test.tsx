import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SecretNameHint, SecretsTable } from "./secretsTab";

const row = { id: "key_1", name: "TAVILY_API_KEY", created_at: "2026-11-01T00:00:00Z", updated_at: "2026-11-02T00:00:00Z" };

describe("secret name hint (wave 4, p02-9)", () => {
	it("states the name rule next to the box", () => {
		expect(renderToStaticMarkup(<SecretNameHint />)).toContain(
			"Capitals, digits and underscores.",
		);
	});
});

describe("secrets author tooltip (wave 4, N33)", () => {
	it("shows the display name as tooltip, never an email", () => {
		const html = renderToStaticMarkup(
			<SecretsTable secrets={[{ ...row, created_by: "a@x.io", created_by_name: "ana" }]} canWrite={false} />,
		);
		expect(html).toContain('title="ana"');
		expect(html).not.toContain('title="a@x.io"');
	});
	it("has no tooltip without a display name", () => {
		const html = renderToStaticMarkup(<SecretsTable secrets={[{ ...row, created_by: "a@x.io" }]} canWrite={false} />);
		expect(html).not.toContain('title="a@x.io"');
	});
});