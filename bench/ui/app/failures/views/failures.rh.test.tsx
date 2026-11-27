import "@/lib/i18n";

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { faultLabel } from "@/components/clusters/clusters.utils";
import { toggleExp } from "@/components/clusters/clustersFilters";
import { LINK_TARGET } from "@/components/clusters/clustersTable";
import { INBOX_LIST_FIXTURE } from "@/components/inbox/inbox.fixtures";
import { InboxTable } from "@/components/inbox/inboxTable";
import { patternName } from "@/components/triage/triageTable";
import i18n from "@/lib/i18n";
import { formatUsd } from "@/lib/money";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { inboxNextStep } from "@/app/inbox/views/inboxConnect";
import { INVESTIGATION_START_CAP_USD } from "@/app/inbox/views/findingsTab";
import { regressionSuite } from "@/app/inbox/views/inboxView";
import "@/app/inbox/views/w443I18n";
import "./ufrhI18n";

const src = (path: string) => readFileSync(resolve(__dirname, "../../..", path), "utf8");

describe("Failures round 2 (lane RH)", () => {
	it("Q-204: the check-fix rerun names its estimate as the cap and always asks first", () => {
		const code = src("components/findings/checkFixDialog.tsx");
		expect(code).toContain("capUsd={estimate.usd}");
		expect(code).not.toContain('confirm="auto"');
		// The confirm text and the label round through the same helper.
		expect(code).toContain("formatUsd(estimate.usd)");
	});

	it("Q-279: Find out why names the cap the dialog starts with", () => {
		expect(i18n.t("investigations.findOutPaid", { ns: "w443", cap: formatUsd(INVESTIGATION_START_CAP_USD) })).toBe(
			"Find out why (paid, up to $1.00 by default)",
		);
		expect(src("components/findings/investigateDialog.tsx")).toContain(`useState("${INVESTIGATION_START_CAP_USD}")`);
	});

	it("Q-25: below sm the inbox renders stacked rows; the wide table hides there", () => {
		const items = INBOX_LIST_FIXTURE.items ?? [];
		const html = renderToStaticMarkup(<InboxTable items={items} onSelect={() => undefined} />);
		expect(html).toContain('data-testid="inbox-list-narrow"');
		expect(html).toMatch(/class="hidden min-w-0 sm:block"/);
		expect(html).toContain("whitespace-normal");
	});

	it("Q-215: task and agent links are at least 24 px tall at 14 px text", () => {
		expect(LINK_TARGET).toContain("min-h-6");
		expect(LINK_TARGET).toContain("text-sm");
		expect(src("components/clusters/clustersTable.tsx")).not.toMatch(/\btext-xs\b/);
	});

	it("Q-217: the comparison picker toggles ids in list order", () => {
		expect(toggleExp("", "e_two", ["e_one", "e_two"])).toBe("e_two");
		expect(toggleExp("e_two", "e_one", ["e_one", "e_two"])).toBe("e_one,e_two");
		expect(toggleExp("e_one,e_two", "e_one", ["e_one", "e_two"])).toBe("e_two");
		expect(toggleExp("gone", "e_one", ["e_one"])).toBe("e_one,gone");
	});

	it("Q-224: clean reads None, a known fault its label, anything else as sent", () => {
		const known = (f: string) => (f === "model-down" ? "model outage" : undefined);
		expect(faultLabel("clean", "None", known)).toBe("None");
		expect(faultLabel("model-down", "None", known)).toBe("model outage");
		expect(faultLabel("heimdall-model-brownout", "None", known)).toBe("heimdall-model-brownout");
		expect(i18n.t("clusters.whichRuns", { ns: "ufrh" })).toBe("Which runs");
	});

	it("Q-226: a pattern has the By agent version name on both tabs", () => {
		const named = (k: string) => (i18n.t(`failures.pattern.${k}` as never, { ns: "agentFirst", defaultValue: "" }) as string) || undefined;
		expect(patternName({ pattern: "false_completion", label: "False completion" }, named)).toBe("Said done but failed");
		expect(patternName({ pattern: "pattern_loop", label: "Loop" }, named)).toBe("Loops");
		expect(patternName({ pattern: "new_one", label: "New one" }, named)).toBe("New one");
		expect(src("components/triage/triageTable.tsx")).not.toContain("{group.pattern}</span>");
	});

	it("Q-211: the test task count links to the benchmark holding most of them", () => {
		expect(regressionSuite([])).toBeNull();
		const items = [{ task: { suite: "a", id: "1" } }, { task: { suite: "b", id: "2" } }, { task: { suite: "b", id: "3" } }, {}];
		expect(regressionSuite(items as never)).toBe("b");
	});

	it("R7-2: the inbox token screen never says ingest token", () => {
		expect(inboxNextStep("generate an ingest token below (org admins), export it as HEIMDALL_INBOX_TOKEN")).toBe(
			"generate an inbox token below (org admins), export it as HEIMDALL_INBOX_TOKEN",
		);
		expect(inboxNextStep("Ingest tokens are listed")).toBe("inbox tokens are listed");
	});
});
