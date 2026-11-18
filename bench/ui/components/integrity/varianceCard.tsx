import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { TONE_CLASS, formatPp, formatSd, formatSignedPp } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { Decision, Verdict } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { bandScale, insideBand, isHarnessNoise } from "./integrity.utils";

export interface VarianceCardProps {
	verdict: Verdict;
	className?: string;
}

/**
 * The noise floor behind the MDE (Eval integrity area, S2): the SD it used and where that came
 * from, the A/A comparison when an A/A arm ran, and the release effect drawn against ±MDE so a
 * reader sees at once whether the experiment could tell the effect from noise.
 */
export function VarianceCard({ verdict, className }: VarianceCardProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const v = verdict.variance;
	const source = v?.source ?? (verdict.mde_assumed_sd ? "assumed" : "measured_aa");
	const noise = isHarnessNoise(v?.aa_decision);
	const sourceTone = noise ? "bad" : source === "assumed" ? "warn" : "good";

	return (
		<Card className={cn("gap-4", className)} data-testid="variance-card" data-source={source}>
			<CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
				<div className="flex flex-col gap-1.5">
					<CardTitle>{t("integrity.variance.title")}</CardTitle>
					<CardDescription>{t("integrity.variance.description")}</CardDescription>
				</div>
				<Badge variant="outline" className={TONE_CLASS[sourceTone]} data-testid="variance-source">
					{t(`integrity.variance.source.${source}`)}
				</Badge>
			</CardHeader>
			<CardContent className="flex flex-col gap-5">
				<NoiseBand
					releaseDBar={verdict.effect?.d_bar}
					aaDBar={v?.aa_effect?.d_bar}
					mdePp={v?.mde ?? verdict.mde}
					releaseLabel={t("integrity.variance.band.release")}
					aaLabel={t("integrity.variance.band.aa")}
				/>
				<dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3 xl:grid-cols-4">
					<Stat label={t("integrity.variance.sdUsed")} testId="variance-sd">
						{formatSd(v?.sd ?? verdict.sd, locale)}
					</Stat>
					<Stat label={t("integrity.variance.measuredSd")} testId="variance-measured-sd">
						{typeof v?.measured_sd === "number" ? formatSd(v.measured_sd, locale) : <Muted>{t("integrity.variance.notMeasured")}</Muted>}
					</Stat>
					<Stat label={t("integrity.variance.assumedSd")} testId="variance-assumed-sd">
						{formatSd(v?.assumed_sd ?? 0.3, locale)}
					</Stat>
					<Stat label={t("integrity.variance.mde")} testId="variance-mde">
						{formatPp(v?.mde ?? verdict.mde, locale)} pp
					</Stat>
					{v?.aa_arm && (
						<>
							<Stat label={t("integrity.variance.aaTasks")} testId="variance-aa-tasks">
								{v.aa_tasks ?? 0}
							</Stat>
							<Stat label={t("integrity.variance.aaEffect")} testId="variance-aa-effect">
								{v.aa_effect ? `${formatSignedPp(v.aa_effect.d_bar, locale)} pp` : <Muted>{t("integrity.variance.notMeasured")}</Muted>}
							</Stat>
							{v.aa_decision && (
								<Stat label={t("integrity.variance.aaDecision")} testId="variance-aa-decision">
									<AADecision decision={v.aa_decision} />
								</Stat>
							)}
						</>
					)}
				</dl>
				{noise && (
					<p role="alert" className={cn("rounded-sm border px-3 py-2 text-sm", TONE_CLASS.bad)} data-testid="variance-harness-noise">
						{t("integrity.variance.harnessNoise")}
					</p>
				)}
				<div className="text-muted-foreground flex flex-col gap-1 text-xs" data-testid="variance-detail">
					{v?.detail && <p>{v.detail}</p>}
					{v && !v.aa_arm && !v.detail && <p>{t("integrity.variance.noAA")}</p>}
					{!v && (
						<p>
							{t("integrity.variance.legacy", {
								kind: verdict.mde_assumed_sd ? t("integrity.variance.legacyAssumed") : t("integrity.variance.legacyMeasured"),
								sd: formatSd(verdict.sd, locale),
							})}
						</p>
					)}
				</div>
			</CardContent>
		</Card>
	);
}

/**
 * A number line in percentage points: the ±MDE band shaded around zero, the release effect as a
 * filled marker and the A/A difference as a hollow one. Inside the band means "cannot tell".
 */
export function NoiseBand({
	releaseDBar,
	aaDBar,
	mdePp,
	releaseLabel,
	aaLabel,
}: {
	releaseDBar?: number;
	aaDBar?: number;
	mdePp: number;
	releaseLabel: string;
	aaLabel: string;
}) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const scale = bandScale(mdePp, [releaseDBar, aaDBar]);
	const inside = insideBand(releaseDBar, mdePp);
	const markerTone = inside === null ? "bg-muted-foreground" : inside ? "bg-chart-warning" : "bg-chart-info";
	const summary = releaseDBar === undefined ? "" : inside ? t("integrity.variance.band.inside") : t("integrity.variance.band.outside");

	return (
		<figure className="flex flex-col gap-2" data-testid="variance-band" data-inside={inside === null ? "unknown" : String(inside)}>
			<figcaption className="text-muted-foreground text-xs font-medium tracking-[0.06em] uppercase">
				{t("integrity.variance.band.label")}
			</figcaption>
			<div className="relative h-14" role="img" aria-label={summary || t("integrity.variance.band.label")}>
				<div className="bg-chart-track absolute inset-x-0 top-6 h-px" />
				{mdePp > 0 && (
					<div
						className="border-chart-warning/50 bg-chart-warning/15 absolute top-3 h-7 rounded-sm border border-dashed"
						style={{ left: `${scale.pos(-mdePp)}%`, width: `${scale.pos(mdePp) - scale.pos(-mdePp)}%` }}
						data-testid="variance-band-mde"
					/>
				)}
				<div className="bg-foreground/50 absolute top-2 h-9 w-px" style={{ left: `${scale.pos(0)}%` }} />
				{aaDBar !== undefined && (
					<Marker
						left={scale.pos(aaDBar * 100)}
						className="border-foreground bg-background size-3 rotate-45 rounded-[1px] border-2"
						testId="variance-band-aa"
					/>
				)}
				{releaseDBar !== undefined && (
					<Marker
						left={scale.pos(releaseDBar * 100)}
						className={cn("ring-background size-3.5 rounded-full ring-2", markerTone)}
						testId="variance-band-release"
					/>
				)}
				<span className="text-muted-foreground absolute top-10 left-0 text-[10px] tabular-nums">
					{formatSignedPp(-scale.domain / 100, locale)} pp
				</span>
				<span className="text-muted-foreground absolute top-10 right-0 text-[10px] tabular-nums">
					{formatSignedPp(scale.domain / 100, locale)} pp
				</span>
			</div>
			<div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
				<Legend
					swatch={cn("size-2.5 rounded-full", markerTone)}
					label={releaseDBar === undefined ? releaseLabel : `${releaseLabel} ${formatSignedPp(releaseDBar, locale)} pp`}
				/>
				{aaDBar !== undefined && (
					<Legend swatch="border-foreground size-2.5 rotate-45 border-2" label={`${aaLabel} ${formatSignedPp(aaDBar, locale)} pp`} />
				)}
				{mdePp > 0 && (
					<Legend
						swatch="border-chart-warning/60 bg-chart-warning/20 h-2.5 w-4 border border-dashed"
						label={`${t("integrity.variance.band.mde")} ${formatPp(mdePp, locale)} pp`}
					/>
				)}
				{summary && <span className="text-foreground">{summary}</span>}
			</div>
		</figure>
	);
}

function Marker({ left, className, testId }: { left: number; className: string; testId: string }) {
	return (
		<span className={cn("absolute top-6 -translate-x-1/2 -translate-y-1/2", className)} style={{ left: `${left}%` }} data-testid={testId} />
	);
}

function Legend({ swatch, label }: { swatch: string; label: string }) {
	return (
		<span className="inline-flex items-center gap-1.5">
			<span className={swatch} aria-hidden="true" />
			{label}
		</span>
	);
}

function AADecision({ decision }: { decision: Decision }) {
	const { t } = useTranslation("bench");
	const tone = isHarnessNoise(decision) ? "bad" : decision === "inconclusive" ? "good" : "neutral";
	return (
		<Badge variant="outline" className={TONE_CLASS[tone]} data-decision={decision}>
			{t(`integrity.variance.aaDecisionValue.${decision}`)}
		</Badge>
	);
}

function Stat({ label, testId, children }: { label: string; testId: string; children: ReactNode }) {
	return (
		<div className="flex flex-col gap-0.5" data-testid={testId}>
			<dt className="text-muted-foreground text-xs">{label}</dt>
			<dd className="text-foreground font-semibold tabular-nums">{children}</dd>
		</div>
	);
}

function Muted({ children }: { children: ReactNode }) {
	return <span className="text-muted-foreground font-normal">{children}</span>;
}