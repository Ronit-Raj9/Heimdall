import { outcomeTone } from "@/components/runs/runs.utils";
import type { Outcome, Run, Value } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CheckCircle2, CircleDashed, XCircle } from "lucide-react";
import { useTranslation } from "react-i18next";

/** One arm in the side-by-side diff. */
export interface DiffLane {
  arm: string;
  run: Run;
  metrics: Record<string, Value | undefined>;
}

export type DiffField =
  "outcome" | "steps" | "tokens" | "cost" | "time" | "output";

const NUMERIC: readonly { field: DiffField; metric: string }[] = [
  { field: "steps", metric: "steps" },
  { field: "tokens", metric: "tokens" },
  { field: "cost", metric: "cost_usd" },
  { field: "time", metric: "wall_time_s" },
];

/** A known number, or null when the value is missing or not measured. */
export function knownNumber(value: Value | undefined): number | null {
  return value && value.state === "observed" && typeof value.v === "number"
    ? value.v
    : null;
}

/** Formats one numeric field for a diff cell; "-" when unknown. */
export function formatDiffValue(field: DiffField, v: number | null): string {
  if (v === null) return "-";
  switch (field) {
    case "cost":
      return `$${v < 1 ? v.toFixed(4) : v.toFixed(2)}`;
    case "time":
      return v >= 60
        ? `${Math.floor(v / 60)}m ${Math.round(v % 60)}s`
        : `${v.toFixed(1)}s`;
    default:
      return Math.round(v).toLocaleString("en-US");
  }
}

/** True when the cells of a row are not all the same. */
export function rowDiffers(
  cells: readonly (string | number | null)[],
): boolean {
  return cells.some((c) => c !== cells[0]);
}

/**
 * The run comparison as a diff tool: one column per arm, one row per fact (outcome, steps, tokens,
 * cost, wall time, final output). Rows whose cells differ are tinted, numbers carry a bar against
 * the largest arm.
 */
export function RunDiff({ lanes }: { lanes: readonly DiffLane[] }) {
  const { t } = useTranslation("compareUx");
  const outcomes = lanes.map((l) => l.run.outcome);
  const outputs = lanes.map((l) => l.run.claim?.text?.trim() ?? "");
  const cols = {
    gridTemplateColumns: `minmax(6.5rem, 9rem) repeat(${lanes.length}, minmax(10rem, 1fr))`,
  };

  return (
    <section className="flex flex-col gap-3" data-testid="compare-diff">
      <div className="flex flex-col gap-0.5">
        <h2 className="text-foreground text-base font-semibold tracking-tight">
          {t("diff.title")}
        </h2>
        <p className="text-muted-foreground text-sm">{t("diff.subtitle")}</p>
      </div>
      <div className="bg-card overflow-x-auto rounded-xl border">
        <div
          className="min-w-fit text-sm"
          role="table"
          aria-label={t("diff.title")}
        >
          <div
            className="bg-muted/40 sticky top-0 grid border-b"
            style={cols}
            role="row"
          >
            <div
              className="text-muted-foreground px-4 py-2.5 text-xs font-medium"
              role="columnheader"
            >
              {t("diff.field")}
            </div>
            {lanes.map((l) => (
              <div
                key={l.arm}
                className="px-4 py-2.5 font-semibold"
                role="columnheader"
              >
                {t("diff.arm", { arm: l.arm })}
              </div>
            ))}
          </div>

          <DiffRow
            label={t("diff.outcome")}
            differs={rowDiffers(outcomes)}
            cols={cols}
            testId="compare-diff-outcome"
          >
            {lanes.map((l) => (
              <div key={l.arm} className="px-4 py-3" role="cell">
                <OutcomePill outcome={l.run.outcome} />
              </div>
            ))}
          </DiffRow>

          {NUMERIC.map(({ field, metric }) => {
            const values = lanes.map((l) => knownNumber(l.metrics[metric]));
            const max = Math.max(0, ...values.map((v) => v ?? 0));
            return (
              <DiffRow
                key={field}
                label={t(`diff.${field}`)}
                differs={rowDiffers(values)}
                cols={cols}
                testId={`compare-diff-${field}`}
              >
                {lanes.map((l, i) => {
                  const v = values[i];
                  return (
                    <div
                      key={l.arm}
                      className="flex flex-col gap-1.5 px-4 py-3"
                      role="cell"
                    >
                      <span
                        className="tabular-nums"
                        data-testid={`compare-diff-${field}-${l.arm}`}
                      >
                        {formatDiffValue(field, v)}
                      </span>
                      {v !== null && max > 0 && (
                        <span
                          className="bg-muted h-1.5 w-full overflow-hidden rounded-full"
                          aria-hidden="true"
                        >
                          <span
                            className="bg-primary/70 block h-full rounded-full"
                            style={{
                              width: `${Math.max(2, (100 * v) / max)}%`,
                            }}
                          />
                        </span>
                      )}
                    </div>
                  );
                })}
              </DiffRow>
            );
          })}

          <DiffRow
            label={t("diff.output")}
            differs={rowDiffers(outputs)}
            cols={cols}
            testId="compare-diff-output"
            last
          >
            {lanes.map((l, i) => (
              <div key={l.arm} className="px-4 py-3" role="cell">
                {outputs[i] ? (
                  <p
                    className="text-foreground/90 line-clamp-4 font-mono text-xs leading-relaxed whitespace-pre-wrap"
                    title={outputs[i]}
                  >
                    {outputs[i]}
                  </p>
                ) : (
                  <span className="text-muted-foreground text-xs">
                    {t("diff.noOutput")}
                  </span>
                )}
              </div>
            ))}
          </DiffRow>
        </div>
      </div>
    </section>
  );
}

function DiffRow({
  label,
  differs,
  cols,
  children,
  testId,
  last,
}: {
  label: string;
  differs: boolean;
  cols: React.CSSProperties;
  children: React.ReactNode;
  testId: string;
  last?: boolean;
}) {
  const { t } = useTranslation("compareUx");
  return (
    <div
      className={cn(
        "hover:bg-muted/30 grid transition-colors",
        !last && "border-b",
        differs && "bg-chart-warning/10 hover:bg-chart-warning/15",
      )}
      style={cols}
      role="row"
      data-testid={testId}
      data-differs={differs}
    >
      <div
        className={cn(
          "flex flex-col gap-1 border-l-2 px-4 py-3",
          differs ? "border-chart-warning" : "border-transparent",
        )}
        role="rowheader"
      >
        <span className="text-muted-foreground text-xs font-medium">
          {label}
        </span>
        {differs && (
          <span className="text-chart-warning-ink text-[10px] font-medium uppercase tracking-wide">
            {t("diff.differs")}
          </span>
        )}
      </div>
      {children}
    </div>
  );
}

function OutcomePill({ outcome }: { outcome: Outcome }) {
  const tone = outcomeTone(outcome);
  const Icon =
    tone === "pass" ? CheckCircle2 : tone === "zero" ? XCircle : CircleDashed;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium",
        tone === "pass" && "bg-chart-success/15 text-chart-success-ink",
        tone === "zero" && "bg-destructive/10 text-destructive",
        tone === "unknown" && "bg-muted text-muted-foreground",
      )}
    >
      <Icon className="size-3.5" aria-hidden="true" />
      {outcome.replace(/_/g, " ")}
    </span>
  );
}
