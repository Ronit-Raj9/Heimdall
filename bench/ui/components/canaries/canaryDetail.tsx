import { TriangleAlert } from "lucide-react";
import { EmptyNote, Section } from "@/components/runs/screenStates";
import { Link } from "@tanstack/react-router";
import { EntityLink } from "@/components/shell";
import { TrendsChart } from "@/components/trends/trendsChart";
import { seriesSlots } from "@/components/trends/trends.utils";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { CanaryArmChanges, CanaryDetail, CanaryEquivalence, CanaryMetricTest, CanaryRecord } from "@/lib/types/bench";
import { useMemo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
	formatMetricValue,
	formatP,
	formatShare,
	formatShift,
	formatUSD,
	isTestStatus,
	latestComparedLook,
	latestEquivalence,
	testStatusVariant,
} from "./canary.utils";
import { CanaryStateBadge } from "./canaryList";

export interface CanaryDetailBodyProps {
	detail: CanaryDetail;
	/** Actions beside the heading, e.g. the run button; none in tests. */
	actions?: ReactNode;
}

/**
 * One canary: its state and why, the history of every target (the trends chart, success per look),
 * what changed in the newest compared look, its tests, the provider equivalence report and every
 * look. Pure: the data comes in as props.
 */
export function CanaryDetailBody({ detail, actions }: CanaryDetailBodyProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const s = detail.summary;
	const look = latestComparedLook(detail);
	const eq = latestEquivalence(detail);
	const slots = useMemo(() => seriesSlots(detail.history, [s.name]), [detail.history, s.name]);
	const points = detail.history.reduce((n, l) => n + l.points.length, 0);

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid={`canary-detail-${s.name}`}>
			<header className="flex flex-col gap-2">
				<div className="flex flex-wrap items-center gap-3">
					<h2 className="text-foreground text-lg font-semibold tracking-tight">{s.name}</h2>
					<CanaryStateBadge state={s.state} testId="canary-detail-state" />
					{s.smoke && <Badge variant="outline">{t("canaries.smoke")}</Badge>}
					{actions}
				</div>
				<p className="text-foreground max-w-3xl text-sm" data-testid="canary-detail-reason">
					{s.reason}
				</p>
				<p className="text-muted-foreground text-xs">
					{t("canaries.detail.facts", {
						suite: s.suite,
						agent: s.agent,
						tasks: s.tasks > 0 ? s.tasks : t("canaries.detail.allTasks"),
						trials: s.trials,
						sensitivity: s.sensitivity,
						alpha: s.alpha,
					})}
				</p>
				<p className="text-muted-foreground text-xs" data-testid="canary-detail-baseline">
					{s.baseline
						? t("canaries.detail.baseline", {
								exp: s.baseline.exp_id,
								at: formatDateTime(s.baseline.pinned_at, locale) ?? s.baseline.pinned_at,
								looks: s.looks,
							})
						: t("canaries.detail.noBaseline", { name: s.name })}
					{s.baseline?.note ? ` ${s.baseline.note}` : ""}
				</p>
			</header>

			<Section title={t("canaries.history.title")} description={t("canaries.history.description")} testId="canary-history">
				{points < 2 ? (
					<EmptyNote testId="canary-history-empty">{t("canaries.history.empty")}</EmptyNote>
				) : (
					<TrendsChart lineages={detail.history} metric="verified_success" slots={slots} />
				)}
			</Section>

			<Section title={t("canaries.changes.title")} description={t("canaries.changes.description")} testId="canary-changes">
				{look?.changes ? <WhatChanged look={look} /> : <EmptyNote testId="canary-changes-empty">{t("canaries.changes.empty")}</EmptyNote>}
			</Section>

			{look?.comparison && (
				<Section
					title={t("canaries.tests.title")}
					description={t("canaries.tests.description", { alpha: look.comparison.alpha })}
					testId="canary-tests"
				>
					<TestsTable tests={look.comparison.tests} locale={locale} />
				</Section>
			)}

			{detail.pairs.length > 0 && (
				<Section title={t("canaries.equivalence.title")} description={t("canaries.equivalence.description")} testId="canary-equivalence">
					{eq?.equivalence ? (
						eq.equivalence.map((e) => <EquivalenceCard key={`${e.arm_a}-${e.arm_b}`} e={e} locale={locale} />)
					) : (
						<EmptyNote testId="canary-equivalence-empty">{t("canaries.equivalence.empty")}</EmptyNote>
					)}
				</Section>
			)}

			<Section title={t("canaries.looks.title")} description={t("canaries.looks.description")} testId="canary-looks">
				<LooksTable records={detail.records} locale={locale} />
			</Section>
		</div>
	);
}

/** The headline lines an alert carries, then per target the evidence behind them. */
function WhatChanged({ look }: { look: CanaryRecord }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const ch = look.changes;
	if (!ch) return null;
	return (
		<div className="flex flex-col gap-4">
			{ch.moved.length > 0 ? (
				<ul className="flex flex-col gap-1 text-sm" data-testid="canary-moved">
					{ch.moved.map((m) => (
						<li key={m} className="flex items-start gap-2">
							<TriangleAlert className="text-warning-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
							<span>{m}</span>
						</li>
					))}
				</ul>
			) : (
				<p className="text-muted-foreground text-sm" data-testid="canary-moved-none">
					{t("canaries.changes.nothing")}
				</p>
			)}
			<Table aria-label={t("canaries.changes.label")} data-testid="canary-changes-table">
				<TableHeader>
					<TableRow>
						<TableHead>{t("canaries.changes.columns.target")}</TableHead>
						<TableHead>{t("canaries.changes.columns.served")}</TableHead>
						<TableHead>{t("canaries.changes.columns.provider")}</TableHead>
						<TableHead className="text-right">{t("canaries.changes.columns.success")}</TableHead>
						<TableHead className="text-right">{t("canaries.changes.columns.latency")}</TableHead>
						<TableHead className="text-right">{t("canaries.changes.columns.prompt")}</TableHead>
						<TableHead className="text-right">{t("canaries.changes.columns.errors")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{ch.arms.map((a) => (
						<ArmRow key={a.arm} a={a} locale={locale} />
					))}
				</TableBody>
			</Table>
			{ch.new_clusters.length > 0 && (
				<div className="flex flex-col gap-1" data-testid="canary-new-clusters">
					<h3 className="text-sm font-medium">{t("canaries.changes.newClusters")}</h3>
					<ul className="text-xs">
						{ch.new_clusters.map((c) => (
							<li key={c.signature}>
								<Link
									to="/inbox"
									search={{ tab: "clusters", sig: c.signature } as never}
									className="font-mono underline-offset-2 hover:underline"
									data-testid={`canary-cluster-${c.signature}`}
								>
									{c.signature}
								</Link>{" "}
								{c.title} ({t("canaries.changes.clusterRuns", { count: c.count })})
							</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

function ArmRow({ a, locale }: { a: CanaryArmChanges; locale: string }) {
	const { t } = useTranslation("bench");
	const unknown = t("canaries.unknown");
	const before = (xs: string[]) => (xs.length > 0 ? xs.join(", ") : unknown);
	return (
		<TableRow data-testid={`canary-change-${a.arm}`} className="align-top">
			<TableCell className="font-mono text-xs">{a.label}</TableCell>
			<TableCell className="text-xs">
				<ChangeCell
					changed={a.served_changed}
					before={before(a.served_before)}
					now={before(a.served_now)}
					testId={`canary-served-${a.arm}`}
				/>
				{a.fingerprint_changed && (
					<div className="text-muted-foreground mt-1">
						{t("canaries.changes.fingerprint", { before: before(a.fingerprints_before), now: before(a.fingerprints_now) })}
					</div>
				)}
			</TableCell>
			<TableCell className="text-xs">
				<ChangeCell changed={a.provider_changed} before={before(a.providers_before)} now={before(a.providers_now)} />
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				{formatShare(a.success_before, locale) ?? unknown} → {formatShare(a.success_now, locale) ?? unknown}
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				{formatMetricValue("latency_ms", a.latency_now_ms, locale) ?? unknown}
				{a.latency_shift !== null && <span className="text-muted-foreground"> ({formatShift(a.latency_shift, locale)})</span>}
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				{a.prompt_tasks > 0 ? t("canaries.changes.promptChanged", { changed: a.prompt_changed, tasks: a.prompt_tasks }) : unknown}
			</TableCell>
			<TableCell className="text-right text-xs tabular-nums">
				{formatShare(a.error_rate_before, locale, 1)} → {formatShare(a.error_rate_now, locale, 1)}
			</TableCell>
		</TableRow>
	);
}

/** before → now, marked when it changed. Arrow text for screen readers is the word "to". */
function ChangeCell({ changed, before, now, testId }: { changed: boolean; before: string; now: string; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!changed) return <span data-testid={testId}>{now}</span>;
	return (
		<span data-testid={testId} data-changed="true">
			<Badge variant="warning" className="mr-1">
				{t("canaries.changes.changed")}
			</Badge>
			<span className="font-mono">{before}</span> <span aria-label={t("canaries.changes.to")}>→</span>{" "}
			<span className="font-mono">{now}</span>
		</span>
	);
}

function TestStatusBadge({ status }: { status: string }) {
	const { t } = useTranslation("bench");
	return (
		<Badge variant={testStatusVariant(status)} data-status={status}>
			{isTestStatus(status) ? t(`canaries.testStatus.${status}`) : status}
		</Badge>
	);
}

function TestsTable({ tests, locale }: { tests: readonly CanaryMetricTest[]; locale: string }) {
	const { t } = useTranslation("bench");
	const unknown = t("canaries.unknown");
	return (
		<Table aria-label={t("canaries.tests.label")} data-testid="canary-tests-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("canaries.tests.columns.target")}</TableHead>
					<TableHead>{t("canaries.tests.columns.metric")}</TableHead>
					<TableHead className="text-right">{t("canaries.tests.columns.baseline")}</TableHead>
					<TableHead className="text-right">{t("canaries.tests.columns.current")}</TableHead>
					<TableHead className="text-right">{t("canaries.tests.columns.tasks")}</TableHead>
					<TableHead className="text-right">{t("canaries.tests.columns.p")}</TableHead>
					<TableHead>{t("canaries.tests.columns.status")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{tests.map((tt) => (
					<TableRow key={tt.key} data-testid={`canary-test-${tt.key}`} title={tt.reason}>
						<TableCell className="font-mono text-xs">
							{tt.label} <span className="text-muted-foreground">({tt.condition})</span>
						</TableCell>
						<TableCell className="text-xs">{t(`canaries.metric.${tt.metric}`, { defaultValue: tt.metric })}</TableCell>
						<TableCell className="text-right text-xs tabular-nums">
							{formatMetricValue(tt.metric, tt.baseline, locale) ?? unknown}
						</TableCell>
						<TableCell className="text-right text-xs tabular-nums">{formatMetricValue(tt.metric, tt.current, locale) ?? unknown}</TableCell>
						<TableCell className="text-right text-xs tabular-nums">
							{t("canaries.tests.changed", { changed: tt.discordant, tasks: tt.tasks })}
						</TableCell>
						<TableCell className="text-right font-mono text-xs">{formatP(tt.p) ?? unknown}</TableCell>
						<TableCell className="text-xs">
							<TestStatusBadge status={tt.status} />
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function EquivalenceCard({ e, locale }: { e: CanaryEquivalence; locale: string }) {
	const { t } = useTranslation("bench");
	const variant = e.verdict === "differs" ? "destructive" : e.verdict === "no_difference_detected" ? "success" : "secondary";
	return (
		<div className="flex flex-col gap-2" data-testid={`canary-equivalence-${e.arm_a}-${e.arm_b}`}>
			<div className="flex flex-wrap items-center gap-2 text-sm">
				<span className="font-mono">{e.label_b}</span>
				<span className="text-muted-foreground">{t("canaries.equivalence.vs")}</span>
				<span className="font-mono">{e.label_a}</span>
				<Badge variant={variant} data-verdict={e.verdict}>
					{t(`canaries.equivalence.verdict.${e.verdict}`, { defaultValue: e.verdict })}
				</Badge>
			</div>
			<p className="text-muted-foreground text-xs">{e.reason}</p>
			<TestsTable tests={e.tests} locale={locale} />
		</div>
	);
}

function LooksTable({ records, locale }: { records: readonly CanaryRecord[]; locale: string }) {
	const { t } = useTranslation("bench");
	return (
		<Table aria-label={t("canaries.looks.label")} data-testid="canary-looks-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("canaries.looks.columns.started")}</TableHead>
					<TableHead>{t("canaries.looks.columns.kind")}</TableHead>
					<TableHead>{t("canaries.looks.columns.outcome")}</TableHead>
					<TableHead>{t("canaries.looks.columns.state")}</TableHead>
					<TableHead>{t("canaries.looks.columns.experiment")}</TableHead>
					<TableHead className="text-right">{t("canaries.looks.columns.cost")}</TableHead>
					<TableHead>{t("canaries.looks.columns.alerts")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{records.map((r) => (
					<TableRow key={`${r.id}-${r.started}`} data-testid={`canary-look-${r.id}`} title={r.reason} className="align-top">
						<TableCell className="text-xs">{formatDateTime(r.started, locale) ?? r.started}</TableCell>
						<TableCell className="text-xs">
							{t(`canaries.kind.${r.kind}`, { defaultValue: r.kind })}
							{r.injected && (
								<Badge variant="outline" className="ml-1">
									{t("canaries.looks.injected", { probe: r.injected })}
								</Badge>
							)}
						</TableCell>
						<TableCell className="text-xs">{t(`canaries.outcome.${r.outcome}`, { defaultValue: r.outcome })}</TableCell>
						<TableCell>
							<CanaryStateBadge state={r.state} />
						</TableCell>
						<TableCell className="font-mono text-xs">{r.exp_id ? <EntityLink kind="experiment" id={r.exp_id} /> : "-"}</TableCell>
						<TableCell className="text-right text-xs tabular-nums">{formatUSD(r.cost_usd, locale)}</TableCell>
						<TableCell className="text-xs">{(r.alerts ?? []).join(", ")}</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}