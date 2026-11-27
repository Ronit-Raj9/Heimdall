import "@/lib/i18n";

import type { SuiteHealth, SuiteList, SuiteTask } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SuiteHealthSummary } from "./suiteHealth";
import { healthByTask, type HealthState } from "./suiteHealth.utils";
import { sortTasks } from "./suites.utils";
import { SuiteTaskTable } from "./suiteTaskTable";
import { TaskHealthBlock } from "./taskHealthBlock";

const FIXTURES = path.resolve(__dirname, "../../public/fixtures");
const list = JSON.parse(fs.readFileSync(path.join(FIXTURES, "suites.json"), "utf8")) as SuiteList;
const report = JSON.parse(fs.readFileSync(path.join(FIXTURES, "suites.health.acme-payments.json"), "utf8")) as SuiteHealth;
const acme = list.suites.find((s) => s.id === "acme-payments")!;

const ready: HealthState = { state: "ready", health: report, byTask: healthByTask(report) };
const noStore: HealthState = { state: "unavailable", reason: "no_store", error: { status: 503 } };

// A held-out task as GET /suites serves it when held_out.visible is false: id, version, status and admitted only.
const heldOut: SuiteTask = {
	id: "p09",
	version: "1.0.0",
	title: "",
	project: "",
	kind: "",
	difficulty: "",
	capabilities: [],
	tags: [],
	provenance: null,
	status: "admitted",
	stale: false,
	admitted: true,
	admitted_at: null,
	checks: [],
	hidden_test_files: 0,
	required_tests: 0,
	protected_globs: 0,
	mutants: 0,
	agent_timeout_s: 0,
	split: "held_out",
	redacted: true,
};

const renderTable = (health?: HealthState, tasks: SuiteTask[] = acme.tasks) =>
	renderToStaticMarkup(
		<SuiteTaskTable
			suiteId="acme-payments"
			rows={sortTasks(tasks, { key: "id", dir: "asc" })}
			sort={{ key: "id", dir: "asc" }}
			onSort={() => undefined}
			selected={null}
			onOpen={() => undefined}
			health={health}
		/>,
	);
const row = (html: string, id: string) => html.match(new RegExp(`data-testid="suites-task-row-${id}"[\\s\\S]*?</tr>`))?.[0] ?? "";

describe("health column", () => {
	it("is absent without health, so the table is unchanged", () => {
		expect(renderTable()).not.toContain("suites-head-health");
	});

	it("shows the flaky verifier in red with its evidence count, apart from agent variance", () => {
		const p02 = row(renderTable(ready), "p02");
		expect(p02).toMatch(/data-testid="suites-task-health-p02-flaky_verifier" data-tone="bad">.*Not ready: grader unreliable/);
		expect(p02).toMatch(/data-testid="suites-task-health-p02-quarantined" data-tone="warn">/);
		expect(p02).toContain("Labelled hard, acts easy");
		expect(p02).toContain("Breaks while running (19%)");
		expect(p02).toContain("Runs out of time (19%)");
		// Variance is a muted line, never a toned badge.
		expect(p02).toMatch(
			/<span class="text-muted-foreground text-xs"[^>]*data-testid="suites-task-health-p02-variance">Agents mixed on it: 1 of 2/,
		);
	});

	it("says no information for a task every config always passes", () => {
		const p03 = row(renderTable(ready), "p03");
		expect(p03).toMatch(/data-testid="suites-task-health-p03-no_discrimination" data-tone="warn">.*Does not tell agents apart/);
		expect(p03).not.toContain("suites-task-health-p03-saturated");
	});

	it("shows no issues for a healthy task with ordinary variance", () => {
		const p01 = row(renderTable(ready), "p01");
		expect(p01).toContain("suites-task-health-p01-ok");
		expect(p01).toContain("Agents mixed on it: 2 of 3");
		expect(p01).not.toContain('data-tone="bad"');
	});

	it("shows n/a when health is unavailable, but still shows a known quarantine", () => {
		const quarantined = { ...acme.tasks[0], quarantine: { reason: "manual hold", at: "2026-11-15T10:30:00Z", source: "manual" as const } };
		const html = renderTable(noStore, [quarantined, acme.tasks[1]]);
		expect(row(html, acme.tasks[1].id)).toContain(">not known<");
		expect(row(html, quarantined.id)).toContain(`suites-task-health-${quarantined.id}-quarantined`);
		expect(row(html, quarantined.id)).toContain("manual hold");
	});
});

describe("held-out rows", () => {
	it("show the id and a held out badge, with no detail link and no title", () => {
		const html = renderTable(ready, [...acme.tasks, heldOut]);
		const p09 = row(html, "p09");
		expect(p09).toContain('data-redacted="true"');
		expect(p09).toContain(">p09<");
		expect(p09).toMatch(/data-testid="suites-task-held-out-p09"[^>]*>.*Private/);
		expect(p09).not.toContain("<button");
		expect(p09).toContain("Withheld for a held-out task.");
		expect(p09).toContain("suites-task-admission-p09");
		expect(p09).toContain("suites-task-health-p09-no-runs");
	});

	it("keep the detail link when the server makes held-out tasks visible", () => {
		const visible = { ...acme.tasks[0], split: "held_out" as const };
		const html = row(renderTable(undefined, [visible]), visible.id);
		expect(html).toContain("<button");
		expect(html).toContain(`suites-task-held-out-${visible.id}`);
		expect(html).toContain("shown because the server config sets held_out.visible");
	});
});

describe("SuiteHealthSummary", () => {
	it("shows held-out, quarantined and canary counts with the report's flag counts", () => {
		const html = renderToStaticMarkup(
			<SuiteHealthSummary suite={{ ...acme, held_out_count: 2, quarantined_count: 1, has_canary: true }} health={ready} />,
		);
		expect(html).toMatch(/data-testid="suites-health-held-out".*?>2</);
		expect(html).toMatch(/data-testid="suites-health-quarantined".*?>1</);
		expect(html).toMatch(/data-testid="suites-health-canary".*?>set</);
		expect(html).toMatch(/data-testid="suites-health-count-flaky_verifier" data-tone="bad">.*Unreliable grader: 1/);
		expect(html).toContain("from 74 saved runs across 4 comparisons");
		expect(html).toContain("Easy at or above 80%, hard at or below 20%");
		expect(html).toContain("Synthetic runs count toward infra and verifier checks only");
	});

	it("says health is unavailable on a 503 and keeps the counts", () => {
		const html = renderToStaticMarkup(<SuiteHealthSummary suite={acme} health={noStore} />);
		expect(html).toMatch(/data-testid="suites-health-unavailable" data-reason="no_store"/);
		expect(html).toContain("Health unavailable.");
		expect(html).toContain("No run store");
		expect(html).toMatch(/data-testid="suites-health-held-out".*?>0</);
		expect(html).toMatch(/data-testid="suites-health-canary".*?>none</);
	});

	it("shows the server's message for another failure", () => {
		const failed: HealthState = { state: "unavailable", reason: "error", error: { status: 500, data: { error: { message: "boom" } } } };
		expect(renderToStaticMarkup(<SuiteHealthSummary suite={acme} health={failed} />)).toContain("The health request failed: boom");
	});
});

describe("TaskHealthBlock", () => {
	const block = (taskId: string, health: HealthState = ready) =>
		renderToStaticMarkup(<TaskHealthBlock taskId={taskId} quarantine={null} health={health} />);
	// One sub-block, from its opening tag (which carries the tone classes) to the next sub-block.
	const sub = (html: string, testId: string) => {
		const start = html.lastIndexOf("<div", html.indexOf(`data-testid="${testId}"`));
		const rest = html.slice(start + 4);
		const next = rest.search(
			/<div class="[^"]*" data-testid="suites-task-health-(quarantine|verifier|difficulty|discrimination|infra|variance)"/,
		);
		return next < 0 ? html.slice(start) : html.slice(start, start + 4 + next);
	};

	it("shows a flaky verifier as serious, with evidence count, kinds and runs", () => {
		const html = block("p02");
		const verifier = html.match(/data-testid="suites-task-health-verifier"[\s\S]*?data-testid="suites-task-health-difficulty"/)?.[0] ?? "";
		expect(verifier).toContain("border-danger");
		expect(verifier).toContain('role="alert"');
		expect(verifier).toContain("Flaky verifier: 3 pieces of evidence from 9 checks.");
		expect(verifier).toMatch(/suites-task-health-kind-regrade_disagrees" data-tone="bad">Regrade disagrees: 1/);
		expect(verifier).toMatch(/suites-task-health-kind-same_output_differs" data-tone="bad">Same output, different verdict: 2/);
		expect(verifier).toContain("comparison e_7k2m; runs r_a1s2d3, r_z9x8c7");
	});

	it("highlights a difficulty mismatch with pass rate, interval, runs and configs", () => {
		const difficulty = sub(block("p02"), "suites-task-health-difficulty");
		expect(difficulty).toContain("border-warning");
		expect(difficulty).toMatch(/suites-task-health-declared".*?>hard</);
		expect(difficulty).toMatch(/suites-task-health-observed".*?text-chart-warning-ink font-semibold">easy</);
		expect(difficulty).toMatch(/suites-task-health-pass-rate".*?>92%</);
		expect(difficulty).toContain("Interval (95%)");
		expect(difficulty).toContain("65% to 99%");
		expect(difficulty).toMatch(/suites-task-health-runs".*?>12</);
		expect(difficulty).toMatch(/suites-task-health-configs".*?>2</);
	});

	it("says no information for an always-pass task", () => {
		const html = block("p03");
		expect(html).toContain("No information: every agent config always passes.");
		expect(html).toMatch(/data-state="always_pass"/);
	});

	it("keeps agent variance under its own heading, never in the verifier's red", () => {
		const html = block("p01");
		const variance = html.match(/data-testid="suites-task-health-variance"[\s\S]*$/)?.[0] ?? "";
		expect(variance).toContain("Agent variance (normal)");
		expect(variance).toContain("Normal agent behavior");
		expect(variance).toContain("2 of 3 configs mixed; mean variance 0.17.");
		expect(variance).toContain("6/8 passed (75%)");
		expect(variance).not.toContain("chart-error");
		expect(html).toContain("No disagreement found in 12 checks.");
	});

	it("shows infra and timeout rates", () => {
		const infra = sub(block("p02"), "suites-task-health-infra");
		expect(infra).toContain("19% (3 of 16 attempts)");
		expect(infra).toContain("2 aborted, 1 infra errors, 0 invalid evaluator");
	});

	it("shows the quarantine with reason, source and date", () => {
		const html = block("p02");
		expect(html).toContain("suites-task-health-quarantine");
		expect(html).toContain("verifier graded the same output differently");
		expect(html).toContain("auto, from verifier evidence");
		expect(html).toMatch(/suites-task-health-q-at".*?2026/);
		expect(html).toContain("regrade_disagrees: e_7k2m r_q1w2e3 pass then fail at grader 1.2.0");
	});

	it("says why health is unavailable, and that a task has no runs yet", () => {
		expect(block("p02", noStore)).toContain("No run store on this server");
		expect(block("p99")).toContain("No saved run of this task yet");
	});
});