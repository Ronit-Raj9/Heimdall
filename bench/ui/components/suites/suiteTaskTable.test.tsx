import "@/lib/i18n";

import type { SuiteList } from "@/lib/types/bench";
import fs from "node:fs";
import path from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { sortTasks, type TaskSort } from "./suites.utils";
import { SuiteTaskTable } from "./suiteTaskTable";

const list = JSON.parse(
  fs.readFileSync(
    path.resolve(__dirname, "../../public/fixtures/suites.json"),
    "utf8",
  ),
) as SuiteList;
const acme = list.suites.find((s) => s.id === "acme-payments")!;
const harbor = list.suites.find((s) => s.id === "harbor-shell")!;

const render = (
  sort: TaskSort = { key: "id", dir: "asc" },
  tasks = acme.tasks,
  selected: string | null = null,
) =>
  renderToStaticMarkup(
    <SuiteTaskTable
      suiteId="acme-payments"
      rows={sortTasks(tasks, sort)}
      sort={sort}
      onSort={() => undefined}
      selected={selected}
      onOpen={() => undefined}
      moreColumns
    />,
  );

const row = (html: string, id: string) =>
  html.match(
    new RegExp(`data-testid="suites-task-row-${id}"[\\s\\S]*?</tr>`),
  )?.[0] ?? "";

describe("SuiteTaskTable", () => {
  it("shows one row per task with its admission state and checks", () => {
    const html = render();
    expect(html).toContain('aria-label="Tasks of acme-payments"');
    expect(row(html, "p01")).toMatch(
      /data-testid="suites-task-admission-p01" data-tone="good">.*Ready/,
    );
    expect(row(html, "p01")).toContain("10 of 10 checks passed");
    expect(row(html, "p02")).toMatch(
      /data-tone="warn">.*Not ready: files changed/,
    );
    expect(row(html, "p03")).toMatch(/data-tone="bad">.*Not ready/);
    expect(row(html, "p03")).toContain("9 of 10 checks passed");
    expect(row(html, "p01")).toContain(">proprietary<");
    expect(row(html, "p01")).toContain("3 files, 5 required");
  });

  it("flags a missing license and an unadmitted task", () => {
    const html = render(undefined, harbor.tasks);
    expect(row(html, "h02")).toMatch(
      /data-testid="suites-task-license-h02"><span class="text-chart-warning-ink">none</,
    );
    expect(row(html, "h01")).toContain("readiness check not run yet");
  });

  it("opens a task from a real button that reports whether it is expanded", () => {
    const html = render(undefined, acme.tasks, "p02");
    expect(row(html, "p02")).toContain('data-state="selected"');
    expect(row(html, "p02")).toMatch(
      /<button type="button" aria-expanded="true" aria-label="Open task p02: Retry idempotent webhook deliveries with backoff"/,
    );
    expect(row(html, "p01")).toContain('aria-expanded="false"');
  });

  it("sets aria-sort on the sorted column only and keeps every sortable header a button", () => {
    const html = render({ key: "difficulty", dir: "desc" });
    expect(html).toMatch(
      /aria-sort="descending"[^>]*data-testid="suites-head-difficulty"/,
    );
    expect(html).toMatch(/aria-sort="none"[^>]*data-testid="suites-head-id"/);
    expect(html.match(/aria-sort="(ascending|descending)"/g)).toHaveLength(1);
    expect(html).toMatch(
      /<button type="button" aria-label="Sort by Difficulty, sorted descending"/,
    );
    const order = [
      ...html.matchAll(/data-testid="suites-task-row-(\w+)"/g),
    ].map((m) => m[1]);
    expect(order).toEqual(["p02", "p01", "p03"]);
  });
});
describe("SuiteTaskTable default columns", () => {
  it("shows only task, difficulty and ready by default; expert columns wait behind More columns", () => {
    const sort: TaskSort = { key: "id", dir: "asc" };
    const html = renderToStaticMarkup(
      <SuiteTaskTable
        suiteId="acme-payments"
        rows={acme.tasks}
        sort={sort}
        onSort={() => undefined}
        selected={null}
        onOpen={() => undefined}
      />,
    );
    expect(html).toContain('data-testid="suites-head-difficulty"');
    expect(html).toContain('data-testid="suites-head-admission"');
    expect(html).not.toContain('data-testid="suites-head-kind"');
    expect(html).not.toContain('data-testid="suites-head-license"');
    expect(html).not.toContain("Skills tested");
  });
});
