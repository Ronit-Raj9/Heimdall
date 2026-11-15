import "@/lib/i18n";

import type { SuiteList, SuiteTask } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ImportedFrom, TaskScenario, hasTaskScenario } from "./suiteScenario";

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/suites.json"), "utf8")) as SuiteList;
const suite = (id: string) => list.suites.find((s) => s.id === id)!;
const task = (suiteId: string, id: string) => suite(suiteId).tasks.find((t) => t.id === id)!;
const render = (t: SuiteTask, suiteOutput?: "diff" | "answer" | "state") =>
	renderToStaticMarkup(<TaskScenario task={t} suiteOutput={suiteOutput} />);

describe("ImportedFrom", () => {
	it("shows benchmark, source, license, revision, importer and when, as text", () => {
		const html = renderToStaticMarkup(<ImportedFrom record={suite("memory-recall").imported!} />);
		expect(html).toMatch(/data-testid="suites-imported-benchmark".*?recall-example@1/);
		expect(html).toMatch(/data-testid="suites-imported-source".*?https:\/\/example.com\/recall-dataset/);
		expect(html).toMatch(/data-testid="suites-imported-license".*?CC-BY-4.0/);
		expect(html).toMatch(/data-testid="suites-imported-revision".*?4b1d9e2/);
		expect(html).toMatch(/data-testid="suites-imported-importer".*?longmemeval/);
		expect(html).not.toContain("<a ");
		expect(html).toContain('aria-label="Import record"');
	});

	it("says what is not recorded, and flags a missing license", () => {
		const html = renderToStaticMarkup(<ImportedFrom record={{ importer: "x", benchmark: "b", source: "s", license: "", at: "" }} />);
		expect(html).toMatch(/data-testid="suites-imported-revision".*?not recorded/);
		expect(html).toMatch(/data-testid="suites-imported-license".*?text-chart-warning-ink[^>]*>No license recorded/);
	});
});

describe("TaskScenario", () => {
	it("renders nothing for a plain repository task", () => {
		const plain = task("coding-core", "t01");
		expect(hasTaskScenario(plain)).toBe(false);
		expect(render(plain, "diff")).toBe("");
	});

	it("shows the task's own image, services, output and skills", () => {
		const html = render(task("support-desk", "d01"), "state");
		expect(html).toMatch(/data-testid="suites-task-image".*?bench\/task-support-desk-d01:9f2c4a1b7e30/);
		expect(html).toContain("workspace seeded from /app");
		expect(html).toMatch(/data-testid="suites-task-service-crm" data-proxied="true".*?MCP.*?through the tool proxy/);
		expect(html).toContain("exports tickets");
		expect(html).toMatch(/data-testid="suites-task-service-ledger-db" data-proxied="false".*?reached directly.*?hidden from the agent/);
		expect(html).toContain("port 5432");
		expect(html).toMatch(/data-testid="suites-task-output-kind"[^>]*>State/);
		expect(html).toMatch(/data-testid="suites-task-skills".*?refund-policy/);
		expect(html).not.toContain("suites-task-impossible");
		expect(html).not.toContain("suites-task-sessions");
	});

	it("lists sessions in order with what carries over between them", () => {
		const html = render(task("memory-recall", "m01"), "answer");
		expect(html).toContain("<ol");
		const order = [...html.matchAll(/data-testid="suites-task-session-([a-z0-9-]+)"/g)].map((m) => m[1]);
		expect(order).toEqual(["chat-1", "chat-2", "question"]);
		expect(html).toContain("Session 1: chat-1");
		expect(html).toContain("workspace reset to the starting state");
		expect(html).toContain("adds sessions/chat-1 to the workspace");
		expect(html).toContain("300 s budget");
		expect(html).toMatch(/data-testid="suites-task-persists"[^>]*>Carried between sessions: the \/memory volume/);
		expect(html).toContain("answer written to answer.txt");
	});

	it("says when a later session keeps the workspace", () => {
		const base = task("memory-recall", "m01");
		const html = render({ ...base, sessions: [base.sessions![0], { ...base.sessions![1], keep_workspace: true }] });
		expect(html).toContain("keeps the previous workspace");
	});

	it("explains the impossible flag", () => {
		const html = render(task("support-desk", "d02"), "state");
		expect(html).toMatch(/data-testid="suites-task-impossible"[^>]*role="note"|role="note"[^>]*data-testid="suites-task-impossible"/);
		expect(html).toContain("Impossible task");
		expect(html).toContain("A pass can only come from cheating");
		expect(html).toContain("integrity violation");
	});

	it("names the suite's output when the task sets none", () => {
		const html = render({ ...task("support-desk", "d02"), output: undefined }, "state");
		expect(html).toMatch(/data-testid="suites-task-output".*?State.*?from the suite/);
	});
});