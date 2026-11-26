import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SuiteExportMenu } from "./exportMenu";

describe("SuiteExportMenu (N70, N78)", () => {
	it("inline suite export has no disclosure", () => {
		const html = renderToStaticMarkup(<SuiteExportMenu suiteId="coding-core" fixtureMode={false} inline />);
		expect(html).not.toMatch(/<details[^>]*data-testid="suite-export-menu"/);
		expect(html).toContain('data-testid="suite-export-menu"');
		expect(html).toContain('data-testid="export-harbor"');
	});

	it("keeps the command inside a From a terminal block", () => {
		const html = renderToStaticMarkup(<SuiteExportMenu suiteId="coding-core" fixtureMode={false} inline />);
		const terminal = html.match(/<details[^>]*data-testid="export-harbor-cli"[\s\S]*?<\/details>/);
		expect(terminal?.[0]).toContain("From a terminal");
		expect(terminal?.[0]).toContain("heimdall export harbor");
		expect(html.replace(terminal?.[0] ?? "", "")).not.toContain("heimdall export");
	});
});