import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { StatusPill } from "@/components/ui/statusPill";
import { Section } from "@/components/runs/screenStates";
import type { FileDiff, ImprovementDetail, RoundView } from "@/lib/types/improve";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ImprovementCurve } from "./improvementCurve";
import { lineageRows, nodeTone, pct, points, usd, verdictTone, versionLabel } from "./improve.utils";

export interface ImprovementDetailBodyProps {
	data: ImprovementDetail;
	/** Approves a paused round with a reason; undefined hides the approval form. */
	onApprove?: (round: number, reason: string) => void;
	approving?: boolean;
}

/** The improvement report: verdict, learning curve, rounds, lineage, integrity and spend. */
export function ImprovementDetailBody({ data, onApprove, approving }: ImprovementDetailBodyProps) {
	const { t } = useTranslation("improve");
	return (
		<div className="flex min-w-0 flex-col gap-8" data-testid="improvement-detail">
			<VerdictCard data={data} />
			<Section title={t("curve.title")} description={t("curve.description")} testId="improvement-curve-section">
				<ImprovementCurve curve={data.curve} band={data.control_band} rounds={data.spec.rounds} />
			</Section>
			<Section title={t("integrity.title")} description={t("integrity.description")} testId="improvement-integrity">
				<IntegrityStrip data={data} />
			</Section>
			<Section title={t("round.title")} testId="improvement-rounds">
				<div className="flex flex-col gap-3">
					{data.round_views.map((r) => (
						<RoundCard key={r.n} round={r} paused={data.status === "awaiting_approval" && r.n === data.round_views.length} onApprove={onApprove} approving={approving} />
					))}
				</div>
			</Section>
			<Section title={t("lineage.title")} description={t("lineage.description")} testId="improvement-lineage">
				<LineageTree data={data} />
			</Section>
			<Section title={t("cost.title")} description={t("cost.description")} testId="improvement-cost">
				<CostTable data={data} />
			</Section>
		</div>
	);
}

function VerdictCard({ data }: { data: ImprovementDetail }) {
	const { t } = useTranslation("improve");
	const v = data.verdict_detail;
	const stats: [string, string][] = v
		? [
				[t("detail.sealedGain"), points(v.sealed_gain)],
				[t("detail.devGain"), points(v.dev_gain)],
				[t("detail.gap"), points(v.generalization_gap)],
				[t("detail.kept"), String(v.commits)],
				[t("detail.rejected"), String(v.rejects)],
				[t("detail.spend"), usd(v.total_usd)],
			]
		: [[t("detail.spend"), usd(data.cost.total)]];
	return (
		<div className="bg-card flex flex-col gap-3 rounded-lg border p-4" data-testid="improvement-verdict">
			<div className="flex flex-wrap items-center gap-2">
				<StatusPill tone={verdictTone(v?.state)}>{t(`verdict.${v?.state ?? "pending"}`)}</StatusPill>
				<span className="text-foreground text-sm font-medium">{v?.headline ?? data.reason}</span>
			</div>
			<dl className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
				{stats.map(([k, val]) => (
					<div key={k} className="flex flex-col gap-0.5">
						<dt className="text-muted-foreground text-xs">{k}</dt>
						<dd className="text-foreground text-sm font-semibold tabular-nums">{val}</dd>
					</div>
				))}
			</dl>
			{v && v.notes.length > 0 && (
				<ul className="text-muted-foreground list-disc pl-5 text-xs">
					{v.notes.map((n) => (
						<li key={n}>{n}</li>
					))}
				</ul>
			)}
			<p className="text-muted-foreground text-xs">
				{t("detail.splits", { dev: data.splits.dev.length, audit: data.splits.audit, sealed: data.splits.sealed, looks: data.audit_looks, max: data.audit_looks_max })}
			</p>
			{data.error && <p className="text-chart-error-ink text-xs">{t("detail.error", { error: data.error })}</p>}
		</div>
	);
}

function IntegrityStrip({ data }: { data: ImprovementDetail }) {
	const { t } = useTranslation("improve");
	return (
		<ol className="flex flex-wrap gap-2" data-testid="improvement-integrity-strip">
			{data.integrity.map((row) => {
				const hits = [...new Set([...row.fired, ...row.tripwires])];
				return (
					<li key={row.round} className={cn("flex min-w-40 flex-col gap-1 rounded-md border p-2 text-xs", hits.length > 0 && "border-chart-error/40")}>
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
						{row.honeypot_passes > 0 && <span className="text-chart-error-ink">{t("integrity.impossible", { n: row.honeypot_passes })}</span>}
					</li>
				);
			})}
		</ol>
	);
}

function RoundCard({ round: r, paused, onApprove, approving }: { round: RoundView; paused: boolean; onApprove?: (round: number, reason: string) => void; approving?: boolean }) {
	const { t } = useTranslation("improve");
	const [reason, setReason] = useState("");
	const decision = r.decision || "pending";
	const tone = r.decision === "committed" ? "ok" : r.decision === "rejected" ? "neutral" : paused ? "warn" : "running";
	const ev = r.audit?.e_value;
	return (
		<article className="flex flex-col gap-2 rounded-lg border p-3" data-testid={`improvement-round-${r.n}`}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="text-foreground text-sm font-semibold">{t("round.heading", { n: r.n })}</span>
				<StatusPill tone={tone}>{t(`round.decision.${decision}`)}</StatusPill>
				<span className="text-muted-foreground text-xs">{t("round.candidate", { parent: versionLabel(r.parent), candidate: versionLabel(r.candidate) })}</span>
				<span className="text-muted-foreground ml-auto text-xs tabular-nums">{t("round.cost", { v: usd(r.cost.improver + r.cost.screen + r.cost.audit + r.cost.sealed) })}</span>
			</div>
			{r.why && <p className="text-foreground text-sm">{r.why}</p>}
			<ul className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums">
				{r.screen && <li>{t("round.practice", { a: pct(r.screen.a.mean), b: pct(r.screen.b.mean) })}</li>}
				{r.audit && ev && <li>{t("round.gate", { a: pct(r.audit.a.mean), b: pct(r.audit.b.mean), e: ev.max_e_up.toFixed(1), threshold: Math.round(ev.threshold) })}</li>}
				{r.sealed && <li>{t("round.heldOut", { v: pct(r.sealed.mean) })}</li>}
			</ul>
			{r.approvals.map((a) => (
				<p key={a.at} className="text-chart-error-ink text-xs" title={a.by || undefined}>
					{t("round.approvedBy", { by: a.by_name || a.by, reason: a.reason })}
				</p>
			))}
			<details className="text-xs">
				<summary className="text-muted-foreground cursor-pointer">{t("round.diff")}</summary>
				{r.diff.length === 0 ? <p className="text-muted-foreground mt-2">{t("round.noDiff")}</p> : r.diff.map((f) => <FileDiffView key={f.path} diff={f} />)}
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
					<Input aria-label={t("round.approveReason")} placeholder={t("round.approveReason")} value={reason} onChange={(e) => setReason(e.target.value)} className="min-w-64 flex-1" />
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
					<div key={i} className={cn(l.op === "+" && "bg-chart-success/10 text-chart-success-ink", l.op === "-" && "bg-chart-error/10 text-chart-error-ink")}>
						{l.op} {l.text}
					</div>
				))}
			</pre>
		</div>
	);
}

function LineageTree({ data }: { data: ImprovementDetail }) {
	const { t } = useTranslation("improve");
	const rows = lineageRows(data.lineage.nodes);
	return (
		<div className="flex flex-col gap-2">
			<StatusPill tone={data.lineage.chain_ok ? "ok" : "bad"} testId="improvement-chain">
				{data.lineage.chain_ok ? t("lineage.chainOk") : t("lineage.chainBroken", { error: data.lineage.chain_error ?? "" })}
			</StatusPill>
			<ul className="flex flex-col gap-1" data-testid="improvement-lineage-tree">
				{rows.map(({ node, depth }) => (
					<li
						key={node.ref}
						className={cn("flex flex-wrap items-center gap-2 text-xs", (node.status === "rejected" || node.status === "control") && "opacity-60")}
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
	const sum = (k: "improver" | "screen" | "audit" | "sealed") => data.cost.rounds.reduce((n, r) => n + r[k], 0);
	const rows: [string, number][] = [
		[t("cost.baseline"), data.cost.baseline],
		[t("cost.improver"), sum("improver")],
		[t("cost.screen"), sum("screen")],
		[t("cost.audit"), sum("audit")],
		[t("cost.sealed"), sum("sealed")],
		[t("cost.controls"), data.cost.controls],
	];
	return (
		<div className="flex flex-col gap-2">
			<table className="w-full max-w-md text-left text-xs tabular-nums">
				<tbody>
					{rows.map(([k, v]) => (
						<tr key={k} className="border-b">
							<td className="text-muted-foreground py-1">{k}</td>
							<td className="text-foreground py-1 text-right">{usd(v)}</td>
						</tr>
					))}
					<tr>
						<td className="text-foreground py-1 font-medium">{t("detail.spend")}</td>
						<td className="text-foreground py-1 text-right font-semibold">{usd(data.cost.total)}</td>
					</tr>
				</tbody>
			</table>
			<p className="text-muted-foreground text-xs">{t("cost.cap", { v: usd(data.cost.improver_cap_per_round) })}</p>
		</div>
	);
}
