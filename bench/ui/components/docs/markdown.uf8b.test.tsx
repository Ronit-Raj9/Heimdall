import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Markdown, appRoute, docLinkLabel } from "./markdown";

describe("guide links (M8-63) and colours (X-68)", () => {
	it("turns app addresses in code into links, not API paths", () => {
		expect(appRoute("/settings?tab=keys")).toBe("/settings?tab=keys");
		expect(appRoute("/docs")).toBe("/docs");
		expect(appRoute("/api/bench/docs")).toBeNull();
		expect(appRoute("GET /runs")).toBeNull();
	});
	it("shows a guide title for a link whose text is the file name", () => {
		const titles = { "web-keys-secrets": "Secrets and keys on the website" };
		expect(docLinkLabel("web-keys-secrets.md", "web-keys-secrets", titles)).toBe("Secrets and keys on the website");
		expect(docLinkLabel("the keys guide", "web-keys-secrets", titles)).toBe("the keys guide");
		expect(docLinkLabel("web-keys-secrets.md", "web-keys-secrets")).toBe("web-keys-secrets.md");
	});
	it("renders both, with token colours on callouts and a scrolling table", () => {
		const html = renderToStaticMarkup(
			<Markdown
				source={"See [web-keys-secrets.md](web-keys-secrets.md) and `/settings?tab=usage`.\n\n> [!WARNING] careful\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"}
				onDocLink={() => undefined}
				docTitles={{ "web-keys-secrets": "Secrets and keys" }}
			/>,
		);
		expect(html).toContain(">Secrets and keys</a>");
		expect(html).toContain('href="/settings?tab=usage"');
		expect(html).toContain("text-chart-warning-ink");
		expect(html).not.toMatch(/(sky|emerald|amber|red)-\d{3}/);
		expect(html).toContain("min-w-[28rem]");
	});
});
