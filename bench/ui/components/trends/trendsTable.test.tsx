import "@/lib/i18n";

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TrendsTable } from "./trendsTable";
import { trendsFixture } from "./trends.fixture";

const data = trendsFixture();

describe("TrendsTable", () => {
	const html = renderToStaticMarkup(<TrendsTable lineages={data.lineages} metric={data.metric} />);

	it("has one row per point", () => {
		expect(html.match(/data-testid="trends-row"/g)).toHaveLength(5);
	});

	it("shows every column the chart summarizes", () => {
		for (const head of [
			"Lineage",
			"Experiment",
			"Date",
			"Arm",
			"Config",
			"Cost per success",
			"Tokens",
			"Wall time",
			"False completion",
			"Verdict",
			"Against previous",
		]) {
			expect(html).toContain(`>${head}</th>`);
		}
	});

	it("shows success with its interval and a short config hash", () => {
		expect(html).toContain("75%");
		expect(html).toContain("[62%, 88%]");
		expect(html).toContain(">67cd7a069ba6");
		expect(html).toContain('title="sha256:67cd7a069ba6');
	});

	it("says changed where the config hash changed", () => {
		expect(html.match(/data-testid="trends-config-changed"/g)).toHaveLength(1);
	});

	it("says not observed instead of zero for a missing number", () => {
		expect(html).toContain("not observed");
	});

	it("shows the verdict, the policy and the release flag", () => {
		expect(html).toContain("regressed");
		expect(html).toContain("policy block");
		expect(html.match(/data-testid="trends-release-flag"/g)).toHaveLength(2);
		expect(html).toContain("Release regressed");
	});

	it("shows each regression status with its reason", () => {
		expect(html).toContain('data-status="regressed"');
		expect(html).toContain('data-status="cannot_tell"');
		expect(html).toContain('data-status="first"');
		expect(html).toContain("3 discordant tasks: the smallest possible p is 0.25, above 0.05");
	});
});