import "@/lib/i18n";

import type { WorkerList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { WorkerStateBadge, WorkersOff, WorkersTable } from "./workersTable";

const list = JSON.parse(fs.readFileSync(path.resolve(__dirname, "../../public/fixtures/workers.json"), "utf8")) as WorkerList;
const row = (html: string, id: string) => html.match(new RegExp(`data-testid="workers-row-${id}"[\\s\\S]*?</tr>`))?.[0] ?? "";

describe("WorkersTable", () => {
	const html = renderToStaticMarkup(<WorkersTable workers={list.workers} />);

	it("is a labelled table with column headers", () => {
		expect(html).toContain('aria-label="Workers"');
		for (const head of [">Worker<", ">Host<", ">Build<", ">State<", ">Running<", ">Completed<", ">Last seen<"])
			expect(html).toContain(head);
		expect(html.match(/scope="col"/g)).toHaveLength(7);
	});

	it("shows each worker's state, slots in use, completed runs and last seen", () => {
		const running = row(html, "w-gpu-1");
		expect(running).toMatch(/data-testid="workers-row-w-gpu-1-state" data-tone="good"[^>]*>.*Running/);
		expect(running).toContain("2 of 4");
		expect(running).toContain('title="s_t01_A_1, s_t02_B_1"');
		expect(running).toContain(">37<");
		expect(running).toContain('dateTime="2026-11-15T09:41:12Z"');
		expect(row(html, "w-gpu-2")).toMatch(/data-tone="neutral"[^>]*>.*Idle/);
		expect(row(html, "w-old")).toMatch(/data-tone="bad"[^>]*>.*Lost/);
		expect(row(html, "w-old")).toContain("0.8.2+3ee0564");
	});
});

describe("WorkerStateBadge", () => {
	it("explains a lost worker and shows an unknown state as sent", () => {
		expect(renderToStaticMarkup(<WorkerStateBadge state="lost" />)).toContain("Its work returns to the queue.");
		expect(renderToStaticMarkup(<WorkerStateBadge state="draining" />)).toContain(">draining<");
	});
});

describe("WorkersOff", () => {
	it("says remote workers are off and how to turn them on, not as an error", () => {
		const html = renderToStaticMarkup(<WorkersOff />);
		expect(html).toContain('data-state="disabled"');
		expect(html).not.toContain('role="alert"');
		expect(html).toContain("Extra workers off");
		expect(html).toContain("BENCH_WORKER_TOKEN");
		expect(html).toContain("heimdall worker -server &lt;url&gt;");
	});
});