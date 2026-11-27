import type { Run, RunDetail } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertOctagon, AlertTriangle, Check, Copy, Lightbulb } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  FAILURE_PREVIEW_CHARS,
  failureHint,
  failurePhase,
  runFailure,
  runReason,
  type RunReason,
} from "./runFailure.utils";
import { sameFailureLink, type RunFailureGroup } from "./runFailureGroup";
import "./uf5aI18n";

/**
 * Why a run did not succeed, at the top of the run Overview: a plain title from the failed stage,
 * the stage in words, the error text (inert, monospace, copyable) and a hint for known patterns.
 * Given the run's detail, a run with no part failure still gets one sentence on why it did not succeed
 * (a tool server error, failed checks, a grade that ran no checks). Renders nothing for a pass.
 */
export function RunFailureCard({
  run,
  detail,
  group,
  testId = "run-failure",
}: {
  run: Pick<Run, "outcome" | "failures">;
  detail?: Pick<RunDetail, "run" | "grade" | "tool_calls" | "sealed">;
  /** The server's failure group (`?failure=1`); when it holds other runs, a link to them follows the card. */
  group?: RunFailureGroup;
  testId?: string;
}) {
  const card = <RunFailureBody run={run} detail={detail} testId={testId} />;
  const same = run.outcome === "pass" ? undefined : sameFailureLink(group);
  if (!same) return card;
  return (
    <div className="flex flex-col gap-2">
      {card}
      <SameFailureLink count={same.count} search={same.search} testId={`${testId}-same`} />
    </div>
  );
}

/** "The same failure in N runs of this evaluation", linking to that failure group. */
function SameFailureLink({ count, search, testId }: { count: number; search: Record<string, string>; testId: string }) {
  const { t } = useTranslation("uf5a");
  return (
    <Link
      to="/inbox"
      search={search as never}
      className="text-primary self-start text-sm underline-offset-2 hover:underline"
      data-testid={testId}
    >
      {t("reason.sameFailure", { count })}
    </Link>
  );
}

function RunFailureBody({
  run,
  detail,
  testId,
}: {
  run: Pick<Run, "outcome" | "failures">;
  detail?: Pick<RunDetail, "run" | "grade" | "tool_calls" | "sealed">;
  testId: string;
}) {
  const { t } = useTranslation("bench");
  const failure = runFailure(run);
  const reason = detail ? runReason(detail) : undefined;
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1400);
    return () => window.clearTimeout(timer);
  }, [copied]);
  if (!failure) return reason ? <RunReasonLine reason={reason} testId={testId} /> : null;

  const message = failure.error.trim();
  const long = message.length > FAILURE_PREVIEW_CHARS;
  const shown =
    long && !expanded ? `${message.slice(0, FAILURE_PREVIEW_CHARS)}…` : message;
  const hint = failureHint(message);
  const phase = failurePhase(failure.stage);
  const stageWords = t(`runs.failure.stage.${failure.stage}`, {
    defaultValue: failure.stage,
  });

  const copy = () => {
    try {
      void navigator.clipboard?.writeText(message).then(
        () => setCopied(true),
        () => undefined,
      );
    } catch {
      // No clipboard (insecure context): the text is still selectable.
    }
  };

  return (
    <div
      role="alert"
      data-testid={testId}
      className="border-destructive/50 bg-destructive/5 text-foreground flex flex-col gap-3 rounded-md border px-4 py-3 text-sm"
    >
      <div className="flex items-start gap-2">
        <AlertOctagon
          className="text-destructive mt-0.5 size-4 shrink-0"
          aria-hidden="true"
        />
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="font-semibold" data-testid={`${testId}-title`}>
            {t(`runs.failure.title.${phase}`)}
          </span>
          <span
            className="text-muted-foreground text-xs"
            data-testid={`${testId}-stage`}
          >
            {t("runs.failure.failedAt", { stage: stageWords })}
            {failure.part ? ` (${failure.part})` : ""}
          </span>
        </div>
      </div>
      <div className="relative">
        <pre
          className="bg-muted/60 max-h-96 overflow-auto rounded-sm px-3 py-2 pr-10 font-mono text-xs break-words whitespace-pre-wrap"
          data-testid={`${testId}-message`}
        >
          {shown}
        </pre>
        <button
          type="button"
          onClick={copy}
          aria-label={t("runs.failure.copy")}
          title={t("runs.failure.copy")}
          className="text-muted-foreground hover:text-foreground absolute top-1.5 right-1.5 rounded-sm p-1"
          data-testid={`${testId}-copy`}
        >
          {copied ? (
            <Check className="size-3.5" aria-hidden="true" />
          ) : (
            <Copy className="size-3.5" aria-hidden="true" />
          )}
        </button>
      </div>
      {long && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="text-primary self-start text-xs underline-offset-2 hover:underline"
          data-testid={`${testId}-toggle`}
        >
          {expanded ? t("runs.failure.showLess") : t("runs.failure.showAll")}
        </button>
      )}
      {hint && (
        <div
          className="flex items-start gap-2 text-xs"
          data-testid={`${testId}-hint`}
        >
          <Lightbulb
            className="text-chart-warning-ink mt-px size-3.5 shrink-0"
            aria-hidden="true"
          />
          <span>{t(`runs.failure.hint.${hint}`)}</span>
        </div>
      )}
    </div>
  );
}

/** Reason kinds that point away from the agent (infrastructure, a grade with no checks): warning tone. */
const WARNING_KINDS: ReadonlySet<RunReason["kind"]> = new Set(["tool", "noChecks", "invalidEvaluator"]);

/** One sentence on why a run without a part failure did not succeed. */
export function RunReasonLine({ reason, testId = "run-failure" }: { reason: RunReason; testId?: string }) {
  const { t } = useTranslation("uf5a");
  const warning = WARNING_KINDS.has(reason.kind);
  const Icon = warning ? AlertTriangle : AlertOctagon;
  const params = reason.kind === "mode" ? { ...reason.params, what: t(`reason.what.${reason.params.what as "files"}`) } : reason.params;
  return (
    <div
      role="status"
      data-testid={testId}
      data-reason={reason.kind}
      className={cn(
        "text-foreground flex items-start gap-2 rounded-md border px-4 py-3 text-sm",
        warning ? "border-chart-warning/50 bg-chart-warning/5" : "border-destructive/50 bg-destructive/5",
      )}
    >
      <Icon className={cn("mt-0.5 size-4 shrink-0", warning ? "text-chart-warning-ink" : "text-destructive")} aria-hidden="true" />
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="font-semibold">{t("reason.title")}</span>
        <span className="break-words" data-testid={`${testId}-reason`}>
          {String(t(`reason.${reason.kind}` as "reason.tool", params as never))}
        </span>
      </div>
    </div>
  );
}
