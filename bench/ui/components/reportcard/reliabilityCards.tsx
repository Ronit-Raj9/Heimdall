import type { Report, ReportCurvePoint, ReportReliabilityCard, ReportResilienceArm } from "@/lib/store/apis/evaluationsApi";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import "./reportCardI18n";
import { ReactReduxContext } from "react-redux";
import { DroppedFaults } from "./droppedFaults";
import { duration, faultName, isAB, pct, ratio, reliabilityCards, usd } from "./reportCard.utils";

const W = 300;
const H = 150;
const PAD = { left: 34, right: 12, top: 10, bottom: 24 };

/**
 * Two lines over k = 1..trials: solved in any of k tries (pass@k, rises) and solved in all k tries
 * (pass^k, falls). The gap between them is labelled flakiness. Numbers are printed beside it.
 */
export function PassCurveChart({ curve, testId }: { curve: ReportCurvePoint[]; testId: string }) {
	if (curve.length === 0) return null;
	const kmin = curve[0].k;
	const kmax = curve[curve.length - 1].k;
	const x = (k: number) => PAD.left + (kmax === kmin ? 0.5 : (k - kmin) / (kmax - kmin)) * (W - PAD.left - PAD.right);
	const y = (v: number) => PAD.top + (1 - Math.max(0, Math.min(1, v))) * (H - PAD.top - PAD.bottom);
	const line = (pick: (p: ReportCurvePoint) => number) => curve.map((p, i) => `${i === 0 ? "M" : "L"}${x(p.k)},${y(pick(p))}`).join(" ");
	const gap = [...curve.map((p) => `${x(p.k)},${y(p.pass_at_k)}`), ...[...curve].reverse().map((p) => `${x(p.k)},${y(p.pass_hat_k)}`)].join(
		" ",
	);
	const last = curve[curve.length - 1];
	const showGap = last.pass_at_k - last.pass_hat_k >= 0.08;
	return (
		<svg
			viewBox={`0 0 ${W} ${H}`}
			className="h-auto w-full max-w-sm"
			role="img"
			aria-label="Solved in any try and in every try, by number of tries"
			data-testid={testId}
		>
			<g aria-hidden="true" className="text-muted-foreground" fontSize={10}>
				{[0, 0.5, 1].map((t) => (
					<g key={t}>
						<line x1={PAD.left} x2={W - PAD.right} y1={y(t)} y2={y(t)} stroke="var(--color-chart-track)" strokeWidth={1} />
						<text x={PAD.left - 6} y={y(t) + 3} textAnchor="end" fill="currentColor">
							{Math.round(t * 100)}%
						</text>
					</g>
				))}
				{curve.map((p) => (
					<text key={p.k} x={x(p.k)} y={H - 8} textAnchor="middle" fill="currentColor">
						{p.k}
					</text>
				))}
			</g>
			<polygon points={gap} fill="var(--color-chart-warning)" fillOpacity={0.15} />
			<path d={line((p) => p.pass_at_k)} fill="none" stroke="var(--color-chart-cat-1)" strokeWidth={2} />
			<path d={line((p) => p.pass_hat_k)} fill="none" stroke="var(--color-chart-cat-2)" strokeWidth={2} strokeDasharray="5 3" />
			{curve.map((p) => (
				<g key={p.k}>
					<circle cx={x(p.k)} cy={y(p.pass_at_k)} r={2.5} fill="var(--color-chart-cat-1)" />
					<circle cx={x(p.k)} cy={y(p.pass_hat_k)} r={2.5} fill="var(--color-chart-cat-2)" />
				</g>
			))}
			{showGap && (
				<text
					x={x(last.k) - 4}
					y={(y(last.pass_at_k) + y(last.pass_hat_k)) / 2 + 3}
					textAnchor="end"
					fontSize={10}
					className="text-muted-foreground"
					fill="currentColor"
				>
					flakiness
				</text>
			)}
		</svg>
	);
}

function Pair({ label, value, testId }: { label: string; value: string; testId?: string }) {
	return (
		<div className="flex justify-between gap-3">
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="text-foreground tabular-nums" data-testid={testId}>
				{value}
			</dd>
		</div>
	);
}

function OneReliability({ benchmark, arm, card, ab }: { benchmark: string; arm: string; card: ReportReliabilityCard; ab: boolean }) {
	const { t: tr } = useTranslation("reportCard");
	const id = `${benchmark}-${arm}`;
	const curve = card.curve ?? [];
	return (
		<figure className="border-border/80 flex min-w-0 flex-col gap-3 rounded-lg border p-4" data-testid={`reliability-card-${id}`}>
			<figcaption className="text-sm font-medium">
				{benchmark}
				{ab && <span className="text-muted-foreground ml-2 text-xs">{arm}</span>}
			</figcaption>
			{curve.length > 1 ? (
				<PassCurveChart curve={curve} testId={`reliability-chart-${id}`} />
			) : (
				<p className="text-muted-foreground text-xs">{tr("reliability.oneTry")}</p>
			)}
			{curve.length > 1 && (
				<ul className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
					<li className="flex items-center gap-1.5">
						<span aria-hidden="true" className="inline-block h-0.5 w-4" style={{ background: "var(--color-chart-cat-1)" }} />
						Solved in any of k tries
					</li>
					<li className="flex items-center gap-1.5">
						<span
							aria-hidden="true"
							className="inline-block h-0.5 w-4 border-t-2 border-dashed"
							style={{ borderColor: "var(--color-chart-cat-2)" }}
						/>
						Solved in all k tries
					</li>
				</ul>
			)}
			<dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-xs sm:grid-cols-2">
				<Pair label={tr("reliability.firstTry")} value={pct(card.pass1)} />
				{/* One try per task measures no flakiness: only first try and cost are shown (M3-7). */}
				{card.k >= 2 && (
					<>
						<Pair label={`Solved in any of ${card.k} tries`} value={pct(card.pass_at_k)} />
						<Pair label={`Solved in all ${card.k} tries`} value={pct(card.pass_hat_k)} />
						<Pair label={tr("reliability.sameEveryTry")} value={pct(card.consistency)} testId={`reliability-consistency-${id}`} />
						<Pair label="Tasks that flip" value={pct(card.flip_rate)} testId={`reliability-flip-${id}`} />
					</>
				)}
				<Pair label={tr("reliability.costPerSolved")} value={usd(card.cost_per_solved)} testId={`reliability-cost-${id}`} />
			</dl>
		</figure>
	);
}

/** One card per benchmark and agent: the pass curve plus consistency, flip rate and cost per solved task. */
export function ReliabilityCards({ report }: { report: Report }) {
	const cards = reliabilityCards(report);
	const ab = isAB(report);
	return (
		<div className="grid gap-4 md:grid-cols-2" data-testid="report-reliability-cards">
			{cards.map((c) => (
				<OneReliability key={`${c.benchmark}-${c.arm}`} benchmark={c.benchmark} arm={c.arm} card={c.card} ab={ab} />
			))}
		</div>
	);
}

/** Degradation bar: share of the clean success rate kept under the fault, with its 95% range. */
function DegradationBar({ m }: { m: ReportResilienceArm }) {
	const scale = (v: number | null) => (Math.max(0, Math.min(1.25, v ?? 0)) / 1.25) * 100;
	if (m.degradation_ratio === null) return <span aria-hidden="true" className="bg-muted block h-2 w-32 rounded-full" />;
	const lo = m.degradation_ratio_low;
	const hi = m.degradation_ratio_high;
	return (
		<span aria-hidden="true" className="bg-muted relative block h-2 w-32 shrink-0 rounded-full">
			<span className="bg-primary/60 absolute inset-y-0 left-0 rounded-full" style={{ width: `${scale(m.degradation_ratio)}%` }} />
			<span
				className="bg-foreground/40 absolute top-1/2 h-px"
				style={{
					left: `${scale(lo)}%`,
					width: `${Math.max(scale(hi) - scale(lo), 0)}%`,
				}}
			/>
			<span className="bg-foreground/60 absolute -top-0.5 h-3 w-px" style={{ left: `${scale(1)}%` }} />
		</span>
	);
}

/**
 * One bar per fault and agent: how much success is kept, recovery rate, cost change and time to recover.
 * Under it, the chosen faults that did not apply to a benchmark and so have no row.
 */
export function ResilienceCard({ report }: { report: Report }) {
	return (
		<div className="flex flex-col gap-3">
			<ResilienceRows report={report} />
			<StoreDroppedFaults evaluationId={report.evaluation} />
		</div>
	);
}

/**
 * The dropped faults read the evaluation from the store; a card drawn without one (a static
 * render) shows nothing here.
 */
export function StoreDroppedFaults({ evaluationId }: { evaluationId: string }) {
	const store = useContext(ReactReduxContext);
	return store ? <DroppedFaults evaluationId={evaluationId} /> : null;
}

function ResilienceRows({ report }: { report: Report }) {
	const rows = report.resilience ?? [];
	const ab = isAB(report);
	return (
		<ul className="border-border/80 divide-border/80 divide-y rounded-lg border" data-testid="report-resilience">
			{rows.flatMap((row) =>
				report.arms
					.filter((arm) => row.arms[arm.label])
					.map((arm) => {
						const m = row.arms[arm.label];
						const range =
							m.degradation_ratio_low !== null && m.degradation_ratio_high !== null
								? ` (${ratio(m.degradation_ratio_low)} to ${ratio(m.degradation_ratio_high)})`
								: m.changed_tasks === 0
									? `, the fault changed no task's result in ${m.tasks} tasks, too few runs for a range`
									: "";
						return (
							<li
								key={`${row.fault}-${arm.label}`}
								className="flex flex-col gap-2 px-4 py-3 text-sm"
								data-testid={`resilience-${row.fault}-${arm.label}`}
							>
								<div className="flex flex-col gap-1 sm:flex-row sm:flex-wrap sm:items-center sm:gap-x-4">
									<span className="text-foreground min-w-0 flex-1 font-medium">
										{faultName(row.fault)}
										{ab && <span className="text-muted-foreground ml-2 text-xs">{arm.label}</span>}
									</span>
									<DegradationBar m={m} />
									<span className="tabular-nums" data-testid={`resilience-ratio-${row.fault}-${arm.label}`}>
										{ratio(m.degradation_ratio)}
										<span className="text-muted-foreground text-xs">{range}</span>
									</span>
								</div>
								<dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-xs sm:grid-cols-4">
									{/* A measure with no data is left out instead of a bare "-" (M3-13). */}
									{[
										{ label: "Recovery rate", value: pct(m.recovery_rate) },
										{ label: "Cost change", value: ratio(m.cost_inflation) },
										{ label: "Time to recover", value: duration(m.time_to_recover_s) },
										{ label: "Waited when asked", value: pct(m.retry_after_respected) },
									]
										.filter((p) => p.value !== "-")
										.map((p) => (
											<Pair key={p.label} label={p.label} value={p.value} />
										))}
								</dl>
							</li>
						);
					}),
			)}
		</ul>
	);
}