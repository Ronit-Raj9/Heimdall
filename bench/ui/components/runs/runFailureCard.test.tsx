import "@/lib/i18n";

import type { PartFailure, Run } from "@/lib/types/bench";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import {
  failureHint,
  failurePhase,
  runFailure,
  shortReason,
} from "./runFailure.utils";
import { RunFailureCard } from "./runFailureCard";

const run = (outcome: Run["outcome"], failures?: PartFailure[]) => ({
  outcome,
  failures,
});

describe("runFailure", () => {
  it("picks the first failure with an error, never for a pass", () => {
    const f = [
      { stage: "prepare" as const, part: "a", error: " " },
      { stage: "grade" as const, part: "b", error: "boom" },
    ];
    expect(runFailure(run("aborted_infra", f))?.part).toBe("b");
    expect(runFailure(run("pass", f))).toBeUndefined();
    expect(runFailure(run("infra_error"))).toBeUndefined();
  });

  it("maps stages to phases and known messages to hints", () => {
    expect(failurePhase("prepare")).toBe("start");
    expect(failurePhase("grade")).toBe("grade");
    expect(failureHint("open /task/x: Permission denied")).toBe("permission");
    expect(failureHint("Error: pull access denied for foo")).toBe("image");
    expect(failureHint("no free key slot for gpt")).toBe("keySlot");
    expect(failureHint("something else")).toBeUndefined();
  });

  it("shortens to the first non-empty line", () => {
    expect(shortReason("\n first line \nsecond")).toBe("first line");
    expect(shortReason("x".repeat(20), 10)).toBe(`${"x".repeat(9)}…`);
  });
});

describe("RunFailureCard", () => {
  it("renders nothing for a pass or a run with no error", () => {
    expect(
      renderToStaticMarkup(
        <RunFailureCard
          run={run("pass", [{ stage: "run", part: "p", error: "x" }])}
        />,
      ),
    ).toBe("");
    expect(
      renderToStaticMarkup(<RunFailureCard run={run("aborted_infra")} />),
    ).toBe("");
  });

  it("says the run could not start, names the stage, shows the error and the permission hint", () => {
    const html = renderToStaticMarkup(
      <RunFailureCard
        run={run("aborted_infra", [
          {
            stage: "prepare",
            part: "container",
            error: "cat /task/a.txt: Permission denied",
          },
        ])}
      />,
    );
    expect(html).toContain("The run could not start");
    expect(html).toContain("preparing the task&#x27;s container");
    expect(html).toContain("cat /task/a.txt: Permission denied");
    expect(html).toContain(
      "This is a setup problem with the benchmark, not your agent.",
    );
    expect(html).toContain('data-testid="run-failure-copy"');
    expect(html).not.toContain("run-failure-toggle");
  });

  it("says it crashed while grading and cuts a long error to 600 characters with Show all", () => {
    const long = `${"a".repeat(600)}TAIL`;
    const html = renderToStaticMarkup(
      <RunFailureCard
        run={run("crash", [{ stage: "grade", part: "grader", error: long }])}
      />,
    );
    expect(html).toContain("The run crashed while grading");
    expect(html).toContain("Show all");
    expect(html).not.toContain("TAIL");
    expect(html).not.toContain("run-failure-hint");
  });

  it("escapes the error as inert text", () => {
    const html = renderToStaticMarkup(
      <RunFailureCard
        run={run("infra_error", [
          { stage: "run", part: "p", error: "<b>x</b> image not found" },
        ])}
      />,
    );
    expect(html).toContain("&lt;b&gt;x&lt;/b&gt;");
    expect(html).toContain("container image is missing on the worker");
  });
});
