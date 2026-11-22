import "@/lib/types/modelSwitchI18n";
import {
  formatShare,
  niceMax,
  observed,
} from "@/components/migration/pareto.utils";
import { getLocale } from "@/lib/i18n";
import type { LatencyPoint } from "@/lib/store/apis/modelSwitchApi";
import { useId } from "react";
import { useTranslation } from "react-i18next";

const W = 560;
const H = 260;
const L = 52;
const R = 16;
const T = 14;
const B = 44;

/**
 * Quality against median model call latency (lane H09), hand-drawn in SVG like the cost chart.
 * Lower latency and higher quality is better, so the frontier runs from the lower left to the
 * upper right. A model with unknown latency or quality is listed as not placed, never drawn at zero.
 */
export function LatencyChart({
  pareto,
  testId = "model-switch-latency-chart",
}: {
  pareto: { points: LatencyPoint[]; frontier: string[]; note: string };
  testId?: string;
}) {
  const { t } = useTranslation("modelSwitch");
  const locale = getLocale();
  const titleId = useId();
  const placed = pareto.points
    .map((p) => ({ p, x: observed(p.latency_ms), y: observed(p.quality) }))
    .filter(
      (d): d is { p: LatencyPoint; x: number; y: number } =>
        d.p.placed && d.x !== null && d.y !== null,
    );
  const xMax = niceMax(Math.max(0, ...placed.map((d) => d.x)));
  const sx = (x: number) => L + (x / xMax) * (W - L - R);
  const sy = (y: number) => H - B - Math.min(1, Math.max(0, y)) * (H - T - B);
  const front = placed.filter((d) => d.p.frontier).sort((a, b) => a.x - b.x);
  const path =
    front.length > 1
      ? front.map((d, i) => `${i ? "L" : "M"}${sx(d.x)},${sy(d.y)}`).join(" ")
      : "";
  const ms = (x: number) => `${Math.round(x)} ms`;
  return (
    <figure className="flex min-w-0 flex-col gap-3" data-testid={testId}>
      <div className="min-w-0 overflow-x-auto">
        <svg
          role="img"
          aria-labelledby={titleId}
          viewBox={`0 0 ${W} ${H}`}
          className="h-auto w-full max-w-3xl min-w-[420px]"
        >
          <title id={titleId}>{pareto.note || t("report.latencyChart")}</title>
          <g aria-hidden="true" className="text-muted-foreground" fontSize={11}>
            {[0, 0.25, 0.5, 0.75, 1].map((y) => (
              <g key={`y-${y}`}>
                <line
                  x1={L}
                  x2={W - R}
                  y1={sy(y)}
                  y2={sy(y)}
                  stroke="var(--color-chart-track)"
                  strokeWidth={1}
                />
                <text
                  x={L - 8}
                  y={sy(y)}
                  dy="0.32em"
                  textAnchor="end"
                  fill="currentColor"
                >
                  {formatShare(y, locale)}
                </text>
              </g>
            ))}
            {[0, 0.5, 1].map((f, i) => (
              <text
                key={`x-${f}`}
                x={sx(f * xMax)}
                y={H - B + 18}
                textAnchor={i === 0 ? "start" : i === 2 ? "end" : "middle"}
                fill="currentColor"
              >
                {ms(f * xMax)}
              </text>
            ))}
            <text
              x={(L + W - R) / 2}
              y={H - 6}
              textAnchor="middle"
              fill="currentColor"
            >
              {t("report.latencyAxis")}
            </text>
          </g>
          {path && (
            <path
              d={path}
              fill="none"
              stroke="var(--color-chart-cat-1)"
              strokeWidth={2}
              strokeDasharray="6 4"
              data-testid={`${testId}-frontier`}
            />
          )}
          {placed.map(({ p, x, y }) => {
            const color = p.frontier
              ? "var(--color-chart-cat-1)"
              : "var(--color-chart-neutral)";
            return (
              <g
                key={p.label}
                data-testid={`${testId}-mark`}
                data-frontier={p.frontier ? "true" : "false"}
              >
                <title>{`${p.model || p.label}: ${ms(x)}, ${formatShare(y, locale)}`}</title>
                {p.frontier ? (
                  <circle
                    cx={sx(x)}
                    cy={sy(y)}
                    r={5}
                    fill={color}
                    stroke="var(--color-background)"
                    strokeWidth={2}
                  />
                ) : (
                  <circle
                    cx={sx(x)}
                    cy={sy(y)}
                    r={4.5}
                    fill="var(--color-background)"
                    stroke={color}
                    strokeWidth={1.5}
                  />
                )}
                <text
                  x={sx(x) + 8}
                  y={sy(y) - 8}
                  fontSize={11}
                  fill="currentColor"
                  className="text-foreground"
                >
                  {p.model || p.label}
                </text>
              </g>
            );
          })}
        </svg>
      </div>
      <figcaption
        className="text-muted-foreground flex flex-col gap-1 text-xs"
        data-testid={`${testId}-points`}
      >
        <ul className="flex flex-col gap-0.5">
          {pareto.points.map((p) => {
            const x = observed(p.latency_ms);
            const y = observed(p.quality);
            return (
              <li
                key={p.label}
                className="text-foreground"
                data-testid={`${testId}-point-${p.label}`}
              >
                {!p.placed || x === null || y === null
                  ? t("report.notPlaced", {
                      label: p.model || p.label,
                      reason: p.reason ?? t("report.unknown"),
                    })
                  : `${p.model || p.label}: ${ms(x)}, ${formatShare(y, locale)}${p.frontier ? ` (${t("report.latencyFrontier")})` : ""}`}
              </li>
            );
          })}
        </ul>
        <p>{pareto.note}</p>
      </figcaption>
    </figure>
  );
}
