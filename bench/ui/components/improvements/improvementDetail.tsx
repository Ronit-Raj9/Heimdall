import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatusPill } from "@/components/ui/statusPill";
import { InlineError, Section } from "@/components/runs/screenStates";
import type { FileDiff, ImprovementDetail, RoundView } from "@/lib/types/improve";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ImprovementCurve } from "./improvementCurve";
import i18n from "@/lib/i18n";
import { lineageRows, money, nodeTone, pct, pointsValue, usd, versionLabel, versionNumber } from "./improve.utils";
import { RoundTimeline } from "./roundTimeline";
import { VerdictCard } from "./verdictCard";
import { VersionExplainSection } from "./versionExplain";
import "./w45impI18n";
import "./ufl7I18n";
import { roundUrl, useLazyCheckImprovementLineageQuery } from "@/lib/store/apis/improveStudyApi";
import { downloadChecked } from "@/lib/downloadChecked";

export interface ImprovementDetailBodyProps {
	data: ImprovementDetail;
	/** Opens the continue dialog; undefined hides Continue (not an admin). */
	onContinue?: () => void;
	/** Approves a paused round with a reason; undefined hides the approval form. */
	onApprove?: (round: number, reason: string) => void;
	approving?: boolean;
}

/**
 * The improvement report (A4.5), top to bottom: verdict, status line, timeline, curve, what changed
 * and why, spend, then the rounds in full, the lineage and the integrity strip.
 */
export function ImprovementDetailBody({ data, onApprove, approving, onContinue }: ImprovementDetailBodyProps) {
	const { t } = useTranslation("improve");
	const { t: tw } = useTranslation("w45imp");
	const line = statusLine(data);
	const kept = data.round_views.flatMap((r) => {
		const n = r.decision === "committed" ? versionNumber(r.candidate) : undefined;
		return n === undefined ? [] : [n];
	});
	return (
		<div className="flex min-w-0 flex-col gap-8" data-testid="improvement-detail">
			<VerdictCard data={data} onContinue={onContinue} />
			{line && (
				<p className="text-foreground text-sm" data-testid="improvement-status-line">
					{line}
				</p>
			)}
			<Section title={tw("timeline.title")} testId="improvement-timeline-section">
				<RoundTimeline rounds={data.round_views} />
			</Section>
			<Section title={t("curve.title")} description={t("curve.description")} testId="improvement-curve-section">
				<ImprovementCurve curve={data.curve} band={data.control_band} rounds={data.spec.rounds} />
			</Section>
			<Section title={tw("explain.title")} testId="improvement-explain-section">
				<VersionExplainSection id={data.id} versions={kept} rounds={data.round_views} />
			</Section>
			<Section title={t("cost.title")} testId="improvement-cost">
				<CostTable data={data} />
			</Section>
			<Section title={t("round.title")} testId="improvement-rounds">
				<div className="flex flex-col gap-3">
					{data.round_views.map((r) => (
						<RoundCard
							key={r.n}
							improvementId={data.id}
							round={r}
							paused={data.status === "awaiting_approval" && r.n === data.round_views.length}
							onApprove={onApprove}
							approving={approving}
						/>
					))}
				</div>
			</Section>
			<Section title={t("lineage.title")} description={t("lineage.description")} testId="improvement-lineage">
				<LineageTree data={data} />
			</Section>
			<Section title={t("integrity.title")} description={t("integrity.description")} testId="improvement-integrity">
				<IntegrityStrip data={data} />
			</Section>
		</div>
	);
}

/** "Round 2 of 5: trying ideas on practice tasks. Spent $3.50 of $60.00." while the run is live (A4.5 item 2). */
export function statusLine(data: ImprovementDetail): string | undefined {
	if (data.status !== "running") return undefined;
	const last = data.round_views[data.round_views.length - 1];
	const r = last ? last.n : Math.min(data.rounds_done + 1, data.rounds);
	const known = ["improve", "screen", "audit", "final", "retries"] as const;
	type Stage = (typeof known)[number] | "other";
	const stage: Stage = last && (known as readonly string[]).includes(last.stage) ? (last.stage as Stage) : "other";
	return i18n.t("status.line", {
		ns: "w45imp",
		r,
		total: data.rounds,
		stage: i18n.t(`status.stage.${stage}`, { ns: "w45imp" }),
		spent: money(data.spent_usd ?? data.cost.total),
		limit: money(data.limit_usd ?? data.spec.budget?.total_usd),
	});
}

function IntegrityStrip({ data }: { data: ImprovementDetail }) {
	const { t } = useTranslation("improve");
	return (
		<ol className="flex flex-wrap gap-2" data-testid="improvement-integrity-strip">
			{data.integrity.map((row) => {
				const hits = [...new Set([...row.fired, ...row.tripwires])];
				return (
					<li
						key={row.round}
						className={cn("flex min-w-40 flex-col gap-1 rounded-md border p-2 text-xs", hits.length > 0 && "border-chart-error/40")}
					>
						<span className="text-foreground font-medium">{t("round.heading", { n: row.round })}</span>
						{hits.length === 0 ? (
							<span className="text-muted-foreground">{t("integrity.clean")}</span>
						) : (
							hits.map((h) => (
								<StatusPill key={h} tone="bad">
									{t(`integrity.gate.${h}`, { defaultValue: h })}
								</StatusPill>
							))
						)}
						{row.overridden && <span className="text-chart-error-ink">{t("integrity.overridden")}</span>}
						{(row.vetoes_incumbent > 0 || row.vetoes_candidate > 0) && (
							<span className="text-muted-foreground">{t("integrity.vetoes", { a: row.vetoes_incumbent, b: row.vetoes_candidate })}</span>
						)}
						{row.honeypot_passes > 0 && (
							<span className="text-chart-error-ink">{t("integrity.impossible", { n: row.honeypot_passes })}</span>
						)}
					</li>
				);
			})}
		</ol>
	);
}

function RoundCard({
	improvementId,
	round: r,
	paused,
	onApprove,
	approving,
}: {
	improvementId: string;
	round: RoundView;
	paused: boolean;
	onApprove?: (round: number, reason: string) => void;
	approving?: boolean;
}) {
	const { t } = useTranslation("improve");
	const [reason, setReason] = useState("");
	const decision = r.decision || "pending";
	const tone = r.decision === "committed" ? "ok" : r.decision === "rejected" ? "neutral" : paused ? "warn" : "running";
	const ev = r.audit?.e_value;
	const { t: t7 } = useTranslation("ufl7");
	const [downloadProblem, setDownloadProblem] = useState<string | null>(null);
	const download = async () => {
		setDownloadProblem(await downloadChecked(roundUrl(improvementId, r.n), `${improvementId}-round-${r.n}.json`));
	};
	return (
		<article className="flex flex-col gap-2 rounded-lg border p-3" data-testid={`improvement-round-${r.n}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="text-foreground text-sm font-semibold">{t("round.heading", { n: r.n })}</span>
				<StatusPill tone={tone}>{t(`round.decision.${decision}`)}</StatusPill>
				<span className="text-muted-foreground text-xs">
					{t("round.candidate", { parent: versionLabel(r.parent), candidate: versionLabel(r.candidate) })}
				</span>
				<span className="text-muted-foreground ml-auto text-xs tabular-nums">
					{t("round.cost", { v: usd(r.cost.improver + r.cost.screen + r.cost.audit + r.cost.sealed) })}
				</span>
				<Button type="button" size="sm" variant="ghost" onClick={() => void download()} data-testid={`improvement-round-download-${r.n}`}>
					{t7("round.download")}
				</Button>
			</div>
			{downloadProblem && (
				<p className="text-chart-error-ink text-xs" role="alert" data-testid={`improvement-round-download-error-${r.n}`}>
					{t7("round.error", { k: r.n })} {downloadProblem}
				</p>
			)}
			{r.why && <p className="text-foreground text-sm">{r.why}</p>}
			<ul className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums">
				{r.screen && <li>{t("round.practice", { a: pct(r.screen.a.mean), b: pct(r.screen.b.mean) })}</li>}
				{r.audit && ev && (
					<li>
						{t("round.gate", {
							a: pct(r.audit.a.mean),
							b: pct(r.audit.b.mean),
							e: ev.max_e_up.toFixed(1),
							threshold: Math.round(ev.threshold),
						})}
					</li>
				)}
				{r.sealed && <li>{t("round.heldOut", { v: pct(r.sealed.mean) })}</li>}
			</ul>
			{r.approvals.map((a) => (
				<p key={a.at} className="text-chart-error-ink text-xs" title={a.by || undefined}>
					{t("round.approvedBy", { by: a.by_name || a.by, reason: a.reason })}
				</p>
			))}
			<details className="text-xs">
				<summary className="text-muted-foreground cursor-pointer">{t("round.diff")}</summary>
				{r.diff.length === 0 ? (
					<p className="text-muted-foreground mt-2">{t("round.noDiff")}</p>
				) : (
					r.diff.map((f) => <FileDiffView key={f.path} diff={f} />)
				)}
			</details>
			{paused && onApprove && (
				<form
					className="flex flex-wrap items-center gap-2"
					onSubmit={(e) => {
						e.preventDefault();
						if (reason.trim()) onApprove(r.n, reason.trim());
					}}
					data-testid="improvement-approve-form"
				>
					<Input
						aria-label={t("round.approveReason")}
						placeholder={t("round.approveReason")}
						value={reason}
						onChange={(e) => setReason(e.target.value)}
						className="min-w-64 flex-1"
					/>
					<Button type="submit" size="sm" disabled={!reason.trim() || approving}>
						{t("round.approveSubmit")}
					</Button>
				</form>
			)}
		</article>
	);
}

function FileDiffView({ diff }: { diff: FileDiff }) {
	return (
		<div className="mt-2 overflow-hidden rounded-md border" data-testid="improvement-file-diff">
			<div className="bg-muted text-muted-foreground flex justify-between px-2 py-1 font-mono text-[11px]">
				<span>{diff.path}</span>
				<span>
					+{diff.added} -{diff.removed}
				</span>
			</div>
			<pre className="max-h-80 overflow-auto p-2 font-mono text-[11px] leading-5">
				{diff.lines.map((l, i) => (
					<div
						key={i}
						className={cn(
							l.op === "+" && "bg-chart-success/10 text-chart-success-ink",
							l.op === "-" && "bg-chart-error/10 text-chart-error-ink",
						)}
					>
						{l.op} {l.text}
					</div>
				))}
			</pre>
		</div>
	);
}

function LineageTree({ data }: { data: ImprovementDetail }) {
	const { t } = useTranslation("improve");
	const { t: t7 } = useTranslation("ufl7");
	const rows = lineageRows(data.lineage.nodes);
	const [check, checked] = useLazyCheckImprovementLineageQuery();
	const chainLine = (lv: { chain_ok: boolean; entries: number; chain_error?: string }): string => {
		if (lv.chain_ok) return t7("lineage.ok", { entries: lv.entries });
		return t7("lineage.broken", { error: lv.chain_error ?? "" });
	};
	return (
		<div className="flex flex-col gap-2">
			<div className="flex flex-wrap items-center gap-2">
				<StatusPill tone={data.lineage.chain_ok ? "ok" : "bad"} testId="improvement-chain">
					{data.lineage.chain_ok ? t("lineage.chainOk") : t("lineage.chainBroken", { error: data.lineage.chain_error ?? "" })}
				</StatusPill>
				<Button
					type="button"
					size="sm"
					variant="outline"
					disabled={checked.isFetching}
					onClick={() => void check(data.id, false)}
					data-testid="improvement-chain-check"
				>
					{checked.isFetching ? t7("lineage.checking") : t7("lineage.check")}
				</Button>
				{checked.data && !checked.isFetching && (
					<span className={cn("text-xs", checked.data.chain_ok ? "text-foreground" : "text-chart-error-ink")} data-testid="improvement-chain-result">
						{chainLine(checked.data)}
					</span>
				)}
			</div>
			<InlineError error={checked.isError ? checked.error : null} title={t7("lineage.error")} testId="improvement-chain-error" />
			<ul className="flex flex-col gap-1" data-testid="improvement-lineage-tree">
				{rows.map(({ node, depth }) => (
					<li
						key={node.ref}
						className={cn(
							"flex flex-wrap items-center gap-2 text-xs",
							(node.status === "rejected" || node.status === "control") && "opacity-60",
						)}
						style={{ paddingLeft: `${depth * 20}px` }}
					>
						<span className="text-foreground font-mono font-medium">{versionLabel(node.ref)}</span>
						<StatusPill tone={nodeTone(node.status)}>{t(`curve.event.${node.status}`)}</StatusPill>
						<span className="text-muted-foreground">{t("lineage.by", { author: t(`lineage.author.${node.author}`) })}</span>
						{node.reason && <span className="text-muted-foreground">{node.reason}</span>}
					</li>
				))}
			</ul>
		</div>
	);
}

function CostTable({ data }: { data: ImprovementDetail }) {
	const { t } = useTranslation("improve");
	const { t: tw } = useTranslation("w45imp");
	const sum = (k: "improver" | "screen" | "audit" | "sealed") => data.cost.rounds.reduce((n, r) => n + r[k], 0);
	const rows: [string, string][] = [
		[t("cost.baseline"), usd(data.cost.baseline)],
		[tw("cost.practice"), usd(sum("screen"))],
		[tw("cost.gate"), usd(sum("audit"))],
		[tw("cost.hidden"), usd(sum("sealed"))],
		[tw("cost.improver"), usd(sum("improver"))],
		[tw("cost.controls"), usd(data.cost.controls)],
	];
	const limit = data.limit_usd ?? data.spec.budget?.total_usd;
	const perDollar = data.verdict_detail?.gain_per_usd;
	const tail: [string, string][] = [];
	if (limit !== undefined && limit > 0) tail.push([tw("cost.hardStop"), usd(limit)]);
	if (perDollar !== undefined) tail.push([tw("cost.perDollar"), tw("cost.perDollarValue", { v: pointsValue(perDollar * 100) })]);
	return (
		<div className="flex flex-col gap-2">
			<table className="w-full max-w-md text-left text-xs tabular-nums">
				<tbody>
					{rows.map(([k, v]) => (
						<tr key={k} className="border-b">
							<td className="text-muted-foreground py-1">{k}</td>
							<td className="text-foreground py-1 text-right">{v}</td>
						</tr>
					))}
					<tr className="border-b">
						<td className="text-foreground py-1 font-medium">{tw("cost.total")}</td>
						<td className="text-foreground py-1 text-right font-semibold">{usd(data.cost.total)}</td>
					</tr>
					{tail.map(([k, v]) => (
						<tr key={k} className="border-b last:border-0">
							<td className="text-muted-foreground py-1">{k}</td>
							<td className="text-foreground py-1 text-right">{v}</td>
						</tr>
					))}
				</tbody>
			</table>
			<p className="text-muted-foreground text-xs">{t("cost.cap", { v: usd(data.cost.improver_cap_per_round) })}</p>
		</div>
	);
}