import { Button } from "@/components/ui/button";
import { StatusPill } from "@/components/ui/statusPill";
import type { ImprovementDetail } from "@/lib/types/improve";
import { useTranslation } from "react-i18next";
import { detectablePoints, points, stateTone, usd } from "./improve.utils";
import "./w45impI18n";

export interface VerdictCardProps {
	data: ImprovementDetail;
	/** Opens the continue dialog; undefined hides the button (not an admin). */
	onContinue?: () => void;
}

/**
 * The verdict (A4.5 item 1): the headline in large type, then the lines under it (A2.12), the
 * numbers, and "Continue with more budget" when the server offers a second look.
 */
export function VerdictCard({ data, onContinue }: VerdictCardProps) {
	const { t } = useTranslation("improve");
	const { t: tw } = useTranslation("w45imp");
	const v = data.verdict_detail;
	const sentences = v?.sentences && v.sentences.length > 0 ? v.sentences : undefined;
	const headline = sentences?.[0] ?? v?.headline ?? data.reason;
	const lines = sentences ? sentences.slice(1) : (v?.notes ?? []);
	const mde = v && v.mde !== undefined ? (detectablePoints(v.mde, v.n) ?? tw("verdict.notEnough")) : undefined;
	const stats: [string, string][] = v
		? [
				[t("detail.sealedGain"), points(v.gain ?? v.sealed_gain)],
				[t("detail.devGain"), points(v.dev_gain)],
				[t("detail.kept"), String(v.commits)],
				[t("detail.rejected"), String(v.rejects)],
				[t("detail.spend"), usd(v.total_usd)],
			]
		: [[t("detail.spend"), usd(data.spent_usd ?? data.cost.total)]];
	if (mde !== undefined) stats.splice(1, 0, [tw("verdict.mde"), mde]);
	const canContinue = data.can_continue === true && onContinue !== undefined;
	return (
		<div className="bg-card flex flex-col gap-3 rounded-lg border p-4" data-testid="improvement-verdict">
			<StatusPill tone={stateTone(v?.state)}>{t(`verdict.${v?.state ?? "pending"}`)}</StatusPill>
			<p className="text-foreground text-lg leading-snug font-semibold" data-testid="verdict-headline">
				{headline || tw("verdict.pending")}
			</p>
			{lines.length > 0 && (
				<ul className="text-muted-foreground flex flex-col gap-1 text-sm" data-testid="verdict-lines">
					{lines.map((n) => (
						<li key={n}>{n}</li>
					))}
				</ul>
			)}
			{canContinue && (
				<div>
					<Button size="sm" onClick={onContinue} data-testid="verdict-continue">
						{tw("verdict.continue")}
					</Button>
				</div>
			)}
			<dl className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
				{stats.map(([k, val]) => (
					<div key={k} className="flex flex-col gap-0.5">
						<dt className="text-muted-foreground text-xs">{k}</dt>
						<dd className="text-foreground text-sm font-semibold tabular-nums">{val}</dd>
					</div>
				))}
			</dl>
			<p className="text-muted-foreground text-xs">
				{t("detail.splits", {
					dev: data.splits.dev.length,
					audit: data.splits.audit,
					sealed: data.splits.sealed,
					looks: data.audit_looks,
					max: data.audit_looks_max,
				})}
			</p>
			{data.error && <p className="text-chart-error-ink text-xs">{t("detail.error", { error: data.error })}</p>}
		</div>
	);
}