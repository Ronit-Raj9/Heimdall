import type { Run } from "@/lib/types/bench";
import { AlertOctagon, Check, Copy, Lightbulb } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  FAILURE_PREVIEW_CHARS,
  failureHint,
  failurePhase,
  runFailure,
} from "./runFailure.utils";

/**
 * Why a run did not succeed, at the top of the run Overview: a plain title from the failed stage,
 * the stage in words, the error text (inert, monospace, copyable) and a hint for known patterns.
 * Renders nothing for a pass or a run that recorded no error.
 */
export function RunFailureCard({
  run,
  testId = "run-failure",
}: {
  run: Pick<Run, "outcome" | "failures">;
  testId?: string;
}) {
  const { t } = useTranslation("bench");
  const failure = runFailure(run);
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1400);
    return () => window.clearTimeout(timer);
  }, [copied]);
  if (!failure) return null;

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
