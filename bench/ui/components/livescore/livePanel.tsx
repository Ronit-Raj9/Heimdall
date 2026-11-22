import { JobRunButton } from "@/components/jobs/jobRunButton";
import { EmptyNote, Section } from "@/components/runs/screenStates";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
	formatE,
	formatSignal,
	inboxLink,
	isLiveResult,
	resultVariant,
	seriesRange,
	seriesValues,
	sortSignalResults,
	sparkPoints,
} from "./live.utils";
import type { LiveComparison, LiveSessionRecord, LiveSignalDef, LiveStatus, LiveVersionView } from "./livescore.types";
import "./fw15I18n";
import "@/components/improvements/gw16I18n";

/** The job kind that compares two live versions; with simulate it runs on made-up sessions. */
export const LIVE_COMPARE_KIND = "livescore.compare";
/** Made-up streams the simulated try compares (the job accepts 1 to 2000). */
export const LIVE_SIMULATE_STREAMS = 200;

/** Colors of the versions' lines, in order; tokens of the shared chart palette. */
const LINE_CLASSES = ["stroke-chart-cat-1", "stroke-chart-cat-2", "stroke-chart-cat-3", "stroke-chart-cat-4", "stroke-chart-cat-5"];

/** A comparison result as a badge. */
export function LiveResultBadge({ result, testId }: { result: string; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<Badge variant={resultVariant(result)} data-result={result} data-testid={testId}>
			{isLiveResult(result) ? t(`live.result.${result}`) : result}
		</Badge>
	);
}

export interface LiveBodyProps {
	status: LiveStatus;
	/** The pull button and its state; omitted in recorded mode and in tests. */
	actions?: ReactNode;
	/** The hosted server: no terminal there, so the "not set up" text names no CLI command. */
	hosted?: boolean;
}

/**
 * The Live page body: what is read (read-only) and what is kept, the comparison of two live versions
 * with sample counts and e-values per signal, every signal over time per version, and the worst
 * sessions linked to their failure inbox items. Every number here is live evidence.
 */
export function LiveBody({ status, actions, hosted }: LiveBodyProps) {
	const { t } = useTranslation("bench");
	const { t: g } = useTranslation("gw16");
	if (!status.configured || status.problem) {
		// A settings file that did not load is named in plain words; the raw reason stays in the title.
		return (
			<div className="flex min-w-0 flex-col gap-4">
				<EmptyNote testId="live-unconfigured">
					{status.problem ? (
						<span title={status.problem}>{t("live.problem")}</span>
					) : (
						<>
							{t("live.unconfigured")}
							{!hosted && <> {g("live.terminal")}</>}
						</>
					)}
				</EmptyNote>
				<SimulatedTry />
			</div>
		);
	}
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="live-body">
			<p
				className="border-chart-warning/60 bg-chart-warning/10 text-foreground rounded-md border px-3 py-2 text-xs leading-relaxed"
				data-testid="live-evidence-note"
			>
				<span className="font-semibold">{t("live.evidenceLabel")}</span> {status.note}
			</p>
			<SourceSection status={status} actions={actions} />
			{status.compare ? (
				<CompareSection compare={status.compare} />
			) : (
				<EmptyNote testId="live-no-compare">{t("live.compare.none")}</EmptyNote>
			)}
			<SeriesSection versions={status.versions} signals={status.signals} />
			<WorstSection worst={status.worst} />
		</div>
	);
}

/**
 * J15.setup: before real keys exist, the comparison can still be tried on made-up sessions (the
 * livescore.compare job with simulate). Nothing reaches the agents or the gateway.
 */
export function SimulatedTry() {
	const { t } = useTranslation("fw15");
	return (
		<div className="flex min-w-0 flex-col gap-2 rounded-xl border p-4" data-testid="live-simulate">
			<p className="text-muted-foreground text-xs">{t("liveSimulate.note")}</p>
			<JobRunButton
				kind={LIVE_COMPARE_KIND}
				params={{ simulate: LIVE_SIMULATE_STREAMS }}
				label={t("liveSimulate.action")}
				variant="outline"
			/>
		</div>
	);
}

function SourceSection({ status, actions }: { status: LiveStatus; actions?: ReactNode }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const src = status.source;
	const g = status.grouping;
	return (
		<Section title={t("live.source.title")} description={t("live.source.description")} testId="live-source" actions={actions}>
			<dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-xs sm:grid-cols-2">
				<div className="flex gap-2">
					<dt className="text-muted-foreground">{t("live.source.gateway")}</dt>
					<dd data-testid="live-source-url">
						{src?.url} {src?.read_only && <Badge variant="outline">{t("live.source.readOnly")}</Badge>}
					</dd>
				</div>
				<div className="flex gap-2">
					<dt className="text-muted-foreground">{t("live.source.lastPull")}</dt>
					<dd data-testid="live-last-pull">
						{formatDateTime(src?.last_pull, locale) ?? t("live.source.never")}
						{src?.last_error && <span className="text-chart-error-ink"> {src.last_error}</span>}
					</dd>
				</div>
				<div className="flex gap-2">
					<dt className="text-muted-foreground">{t("live.source.grouping")}</dt>
					<dd data-testid="live-grouping">
						{t("live.source.groupingCounts", {
							id: g.by_session_id,
							gap: g.by_gap,
							concurrent: g.concurrent,
						})}
					</dd>
				</div>
				<div className="flex gap-2">
					<dt className="text-muted-foreground">{t("live.source.privacy")}</dt>
					<dd data-testid="live-privacy">
						{t("live.source.privacyText", {
							rate: Math.round(status.privacy.sample_rate * 100),
							days: status.privacy.retention_days,
						})}
					</dd>
				</div>
				{status.judge && (
					<div className="flex gap-2 sm:col-span-2">
						<dt className="text-muted-foreground">{t("live.source.judge")}</dt>
						<dd data-testid="live-judge">
							{status.judge.model}: {status.judge.note}
						</dd>
					</div>
				)}
			</dl>
			<p className="text-muted-foreground text-xs">{g.reliability}</p>
		</Section>
	);
}

/** The comparison: the result, the test's level, and one row per signal with pairs, means, e-values. */
export function CompareSection({ compare }: { compare: LiveComparison }) {
	const { t } = useTranslation("bench");
	const rows = sortSignalResults(compare.signals);
	return (
		<Section
			title={t("live.compare.title", {
				candidate: compare.candidate,
				baseline: compare.baseline,
			})}
			description={t("live.compare.description", {
				alpha: compare.alpha,
				signals: compare.signals.length,
				threshold: Math.round(compare.threshold),
				pairs: compare.min_pairs,
			})}
			testId="live-compare"
			actions={<LiveResultBadge result={compare.result} testId="live-compare-result" />}
		>
			<p className="text-xs" data-testid="live-compare-counts">
				{t("live.compare.counts", {
					candidate: compare.sessions_candidate,
					baseline: compare.sessions_baseline,
				})}
				{compare.restarts > 0 && ` ${t("live.compare.restarts", { count: compare.restarts })}`}
			</p>
			{compare.stale && (
				<p className="text-chart-warning-ink text-xs" data-testid="live-compare-stale">
					{compare.stale}
				</p>
			)}
			<Table aria-label={t("live.compare.label")} data-testid="live-compare-table">
				<TableHeader>
					<TableRow>
						<TableHead>{t("live.compare.columns.signal")}</TableHead>
						<TableHead className="text-right">{t("live.compare.columns.pairs")}</TableHead>
						<TableHead className="text-right">{t("live.compare.columns.baseline")}</TableHead>
						<TableHead className="text-right">{t("live.compare.columns.candidate")}</TableHead>
						<TableHead className="text-right">{t("live.compare.columns.eWorse")}</TableHead>
						<TableHead className="text-right">{t("live.compare.columns.eBetter")}</TableHead>
						<TableHead>{t("live.compare.columns.decision")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{rows.map((r) => (
						<TableRow key={r.name} data-testid={`live-signal-${r.name}`}>
							<TableCell>{r.label}</TableCell>
							<TableCell className="text-right tabular-nums">{r.pairs}</TableCell>
							<TableCell className="text-right tabular-nums">{formatSignal(r.mean_baseline, r.kind, r.unit)}</TableCell>
							<TableCell className="text-right tabular-nums">{formatSignal(r.mean_candidate, r.kind, r.unit)}</TableCell>
							<TableCell className="text-right tabular-nums">{formatE(r.e_worse)}</TableCell>
							<TableCell className="text-right tabular-nums">{formatE(r.e_better)}</TableCell>
							<TableCell>
								<LiveResultBadge result={r.decision} testId={`live-decision-${r.name}`} />
								{r.need_pairs > 0 && (
									<span className="text-muted-foreground ml-2 text-xs">{t("live.compare.needPairs", { count: r.need_pairs })}</span>
								)}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
			<p className="text-muted-foreground text-xs leading-relaxed">{compare.method}</p>
		</Section>
	);
}

/** Every signal over time: one row per signal, one line per version, on a shared scale. */
export function SeriesSection({ versions, signals }: { versions: LiveVersionView[]; signals: LiveSignalDef[] }) {
	const { t } = useTranslation("bench");
	if (versions.every((v) => v.series.length === 0)) {
		return <EmptyNote testId="live-series-empty">{t("live.series.empty")}</EmptyNote>;
	}
	const w = 160;
	const h = 28;
	return (
		<Section title={t("live.series.title")} description={t("live.series.description")} testId="live-series">
			<div className="flex flex-wrap gap-3 text-xs" data-testid="live-series-legend">
				{versions.map((v, i) => (
					<span key={v.name} className="flex items-center gap-1">
						<svg width="14" height="4" aria-hidden="true">
							<line x1="0" y1="2" x2="14" y2="2" className={LINE_CLASSES[i % LINE_CLASSES.length]} strokeWidth="2" />
						</svg>
						{t("live.series.legend", {
							name: v.name,
							sessions: v.sessions,
							flagged: v.flagged,
							selector: v.selector,
						})}
					</span>
				))}
			</div>
			<Table aria-label={t("live.series.label")} data-testid="live-series-table">
				<TableHeader>
					<TableRow>
						<TableHead>{t("live.series.columns.signal")}</TableHead>
						{versions.map((v) => (
							<TableHead key={v.name} className="text-right">
								{t("live.series.columns.last24h", { name: v.name })}
							</TableHead>
						))}
						<TableHead>{t("live.series.columns.trend")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{signals.map((def) => {
						const values = versions.map((v) => seriesValues(v.series, def.name));
						const [lo, hi] = seriesRange(values, def);
						return (
							<TableRow key={def.name} data-testid={`live-series-${def.name}`}>
								<TableCell title={def.method}>{def.label}</TableCell>
								{versions.map((v) => {
									const m = v.last_24h.find((x) => x.signal === def.name);
									return (
										<TableCell key={v.name} className="text-right tabular-nums">
											{m ? `${formatSignal(m.mean, def.kind, def.unit)} (${m.n})` : "-"}
										</TableCell>
									);
								})}
								<TableCell>
									<svg
										width={w}
										height={h}
										role="img"
										aria-label={t("live.series.sparkLabel", {
											signal: def.label,
										})}
									>
										{values.map((vs, i) => (
											<polyline
												key={versions[i].name}
												points={sparkPoints(vs, lo, hi, w, h)}
												fill="none"
												strokeWidth="1.5"
												className={LINE_CLASSES[i % LINE_CLASSES.length]}
											/>
										))}
									</svg>
								</TableCell>
							</TableRow>
						);
					})}
				</TableBody>
			</Table>
		</Section>
	);
}

/** The worst sessions by severity, each linked to its inbox item when it was pushed. */
export function WorstSection({ worst }: { worst: LiveSessionRecord[] }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	if (worst.length === 0) {
		return <EmptyNote testId="live-worst-empty">{t("live.worst.empty")}</EmptyNote>;
	}
	return (
		<Section title={t("live.worst.title")} description={t("live.worst.description")} testId="live-worst">
			<Table aria-label={t("live.worst.label")} data-testid="live-worst-table">
				<TableHeader>
					<TableRow>
						<TableHead>{t("live.worst.columns.session")}</TableHead>
						<TableHead>{t("live.worst.columns.version")}</TableHead>
						<TableHead>{t("live.worst.columns.ended")}</TableHead>
						<TableHead>{t("live.worst.columns.flags")}</TableHead>
						<TableHead className="text-right">{t("live.worst.columns.requests")}</TableHead>
						<TableHead>{t("live.worst.columns.inbox")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{worst.map((s) => (
						<TableRow key={s.id} data-testid={`live-worst-${s.id}`}>
							<TableCell className="font-mono text-xs">{s.id}</TableCell>
							<TableCell>{s.version}</TableCell>
							<TableCell>{formatDateTime(s.ended, locale)}</TableCell>
							<TableCell>
								<span className="flex flex-wrap gap-1">
									{(s.flags ?? []).map((f) => (
										<Badge key={f} variant={f === "abandoned" || f === "injection_marker" ? "destructive" : "outline"}>
											{f}
										</Badge>
									))}
								</span>
							</TableCell>
							<TableCell className="text-right tabular-nums">{s.requests}</TableCell>
							<TableCell>
								{s.inbox_item ? (
									<a
										className="text-primary underline-offset-2 hover:underline"
										href={inboxLink(s.inbox_item)}
										data-testid={`live-inbox-${s.id}`}
									>
										{s.inbox_item}
									</a>
								) : (
									<span className="text-muted-foreground">{t("live.worst.notPushed")}</span>
								)}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</Section>
	);
}