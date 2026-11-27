import { BillingUsageSection } from "@/components/billing/billingUsageSection";
import { EmptyState, PageHeader, useEmbedded } from "@/components/shell";
import { Button } from "@/components/ui/button";
import { formatUsd } from "@/lib/money";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetAdminUsageQuery, useGetUsageQuery } from "@/lib/store/apis/usageApi";
import { useSession } from "@/lib/session/sessionContext";
import type { AdminUsageReport, UsageBucket, UsageGroupBy, UsageReport } from "@/lib/types/usage";
import { cn } from "@/lib/utils";
import { Coins, Download, Hash, KeyRound, PlayCircle, type LucideIcon } from "lucide-react";
import { StatCard } from "@/app/operate/views/uxBits";
import { parseAsString, useQueryStates } from "nuqs";
import { useId, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import "@/components/settings/w4m8I18n";
import "@/components/settings/uf8aI18n";

const GROUPS: UsageGroupBy[] = ["day", "model", "agent", "key"];
const CHART_MAX_BARS = 31;

/** YYYY-MM-DD of d in UTC. */
export function isoDay(d: Date): string {
	return d.toISOString().slice(0, 10);
}

/** The first day of now's calendar month, UTC. */
export function monthStart(now: Date): string {
	return `${now.toISOString().slice(0, 7)}-01`;
}

/** The default range: this calendar month up to today (UTC), the same month the key budgets count. */
export function defaultRange(now = new Date()): { from: string; to: string } {
	return { from: monthStart(now), to: isoDay(now) };
}

export type UsagePreset = "month" | "lastMonth" | "7d" | "30d";
export const USAGE_PRESETS: UsagePreset[] = ["month", "lastMonth", "7d", "30d"];

/** The range of a period preset, UTC. */
export function presetRange(preset: UsagePreset, now = new Date()): { from: string; to: string } {
	const day = 24 * 3600 * 1000;
	if (preset === "month") return defaultRange(now);
	if (preset === "lastMonth") {
		const end = new Date(`${monthStart(now)}T12:00:00Z`).getTime() - day;
		return { from: monthStart(new Date(end)), to: isoDay(new Date(end)) };
	}
	const days = preset === "7d" ? 7 : 30;
	return { from: isoDay(new Date(now.getTime() - (days - 1) * day)), to: isoDay(now) };
}

/** The preset a range matches, or undefined for a custom range. */
export function presetOf(range: { from: string; to: string }, now = new Date()): UsagePreset | undefined {
	return USAGE_PRESETS.find((p) => {
		const r = presetRange(p, now);
		return r.from === range.from && r.to === range.to;
	});
}

const DAY_RE = /^\d{4}-\d{2}-\d{2}$/;

/** True for a YYYY-MM-DD that is a real calendar day. */
function realDay(v: string): boolean {
	if (!DAY_RE.test(v)) return false;
	const d = new Date(`${v}T00:00:00Z`);
	return !Number.isNaN(d.getTime()) && isoDay(d) === v;
}

/**
 * The range from the link's from and to (N75): a value counts only when it is a real YYYY-MM-DD day; a reversed range
 * is swapped (swapped is then true, so the page can say so); a missing or ignored from is the first of to's month, a missing or ignored to is today. invalid lists the
 * values that were ignored.
 */
export function rangeFromSearch(
	search: { from?: string | null; to?: string | null },
	now = new Date(),
): { from: string; to: string; invalid: string[]; swapped?: boolean } {
	const invalid: string[] = [];
	const pick = (v?: string | null) => {
		if (!v) return null;
		if (realDay(v)) return v;
		invalid.push(v);
		return null;
	};
	const from = pick(search.from);
	const to = pick(search.to);
	if (!from && !to) return { ...defaultRange(now), invalid };
	if (!from) return { ...defaultRange(new Date(`${to}T12:00:00Z`)), invalid };
	const end = to ?? defaultRange(now).to;
	return from <= end ? { from, to: end, invalid } : { from: end, to: from, invalid, swapped: true };
}

export type UsageScope = "org" | "platform";

/** A link value the page could not use. */
export interface IgnoredParam {
	key: "from" | "to" | "group" | "scope";
	value: string;
}

/** Everything the usage page reads from its link: range, grouping and scope, and what was ignored. */
export function usageSearchState(
	search: { from?: string | null; to?: string | null; group?: string | null; scope?: string | null },
	now: Date,
	serverAdmin: boolean,
): { range: { from: string; to: string }; group: UsageGroupBy; scope: UsageScope; ignored: IgnoredParam[]; swapped: boolean } {
	const r = rangeFromSearch(search, now);
	const ignored: IgnoredParam[] = r.invalid.map((value) => ({ key: value === search.from ? "from" : "to", value }));
	let group: UsageGroupBy = "day";
	if (search.group) {
		if ((GROUPS as string[]).includes(search.group)) group = search.group as UsageGroupBy;
		else ignored.push({ key: "group", value: search.group });
	}
	let scope: UsageScope = "org";
	if (search.scope) {
		if (search.scope === "org" || (search.scope === "platform" && serverAdmin)) scope = search.scope;
		else ignored.push({ key: "scope", value: search.scope });
	}
	return { range: { from: r.from, to: r.to }, group, scope, ignored, swapped: r.swapped === true };
}

/** One line per link value that was ignored, so a shared link never shows other numbers silently. */
export function UsageLinkNotes({ ignored, swapped = false }: { ignored: IgnoredParam[]; swapped?: boolean }) {
	const { t } = useTranslation("usage");
	const { t: tm } = useTranslation("w4m8");
	if (ignored.length === 0 && !swapped) return null;
	const groups = GROUPS.map((g) => t(`group.${g}`));
	const list = `${groups.slice(0, -1).join(", ")} or ${groups[groups.length - 1]}`;
	return (
		<ul className="text-muted-foreground flex flex-col gap-1 text-xs" role="status" data-testid="usage-link-notes">
			{swapped && <li data-testid="usage-link-swapped">{tm("usage.swapped")}</li>}
			{ignored.map((p) => (
				<li key={`${p.key}-${p.value}`} data-testid={`usage-link-ignored-${p.key}`}>
					{p.key === "group"
						? t("link.badGroup", { value: p.value, groups: list, fallback: t("group.day") })
						: p.key === "scope"
							? t("link.badScope", { value: p.value })
							: t("link.badDate", { value: p.value })}
				</li>
			))}
		</ul>
	);
}

/** The usage page's link parameters (N75); a control change writes its parameter back, replacing the history entry. */
const USAGE_QUERY = { from: parseAsString, to: parseAsString, group: parseAsString, scope: parseAsString };

/** Money on this page: the one UI money format (lib/money). */
export function formatUSD(v: number): string {
	return formatUsd(v);
}

/** A day bucket key (YYYY-MM-DD) as "Tue, 10 Nov"; any other key unchanged. */
export function bucketLabel(key: string, locale = "en-GB"): string {
	if (!DAY_RE.test(key)) return key;
	const d = new Date(`${key}T12:00:00Z`);
	if (Number.isNaN(d.getTime())) return key;
	return new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric", month: "short", timeZone: "UTC" }).format(d);
}

/**
 * The table rows: days newest first; model, agent and key by cost, highest first. Rows with no
 * runs and no cost are left out unless showEmpty (a month of days is mostly empty).
 */
export function tableRows(buckets: UsageBucket[], groupBy: UsageGroupBy, showEmpty: boolean): UsageBucket[] {
	const kept = showEmpty ? [...buckets] : buckets.filter((b) => b.runs > 0 || b.cost_usd > 0);
	return groupBy === "day" ? kept.reverse() : kept.sort((a, b) => b.cost_usd - a.cost_usd);
}

function csvCell(v: string | number): string {
	const s = String(v);
	return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

/** The CSV of the current grouping and range: a header line naming the range and the definition, then one row per bucket. */
export function usageCsv(data: Pick<UsageReport, "from" | "to" | "group_by" | "buckets">, definition: string): string {
	const lines = [
		`# ${csvCell(`${data.from} to ${data.to} (UTC), grouped by ${data.group_by}. ${definition}`)}`,
		["group", "label", "cost_usd", "tokens", "runs", "unknown_cost_runs"].join(","),
		...data.buckets.map((b) => [b.key, b.label ?? "", b.cost_usd, b.tokens, b.runs, b.unknown_cost_runs].map(csvCell).join(",")),
	];
	return `${lines.join("\n")}\n`;
}

/** The download name of the CSV. */
export function usageCsvName(data: Pick<UsageReport, "from" | "to" | "group_by">): string {
	return `heimdall-usage-${data.from}-${data.to}-${data.group_by}.csv`;
}

function downloadCsv(name: string, body: string) {
	if (typeof document === "undefined") return;
	const url = URL.createObjectURL(new Blob([body], { type: "text/csv;charset=utf-8" }));
	const a = document.createElement("a");
	a.href = url;
	a.download = name;
	a.click();
	URL.revokeObjectURL(url);
}

export function formatCount(v: number): string {
	return Math.round(v).toLocaleString("en-US");
}

/** Usage page: the org's run cost and tokens over a range, a chart and the table under it. */
export default function UsageView() {
	const { t } = useTranslation("usage");
	const { t: tl } = useTranslation("uf8a");
	const session = useSession();
	const embedded = useEmbedded();
	const [search, setSearch] = useQueryStates(USAGE_QUERY, { history: "replace" });
	const state = useMemo(() => usageSearchState(search, new Date(), session.serverAdmin === true), [search, session.serverAdmin]);
	const { range, group: groupBy, scope, ignored, swapped } = state;
	// The link always holds the sorted pair, so it agrees with the date boxes.
	const setRange = (next: (r: { from: string; to: string }) => { from: string; to: string }) => {
		const r = next(range);
		void setSearch(r.from <= r.to ? { from: r.from, to: r.to } : { from: r.to, to: r.from });
	};
	const setGroupBy = (g: UsageGroupBy) => void setSearch({ group: g === "day" ? null : g });
	const setScope = (sc: UsageScope) => void setSearch({ scope: sc === "org" ? null : sc });
	const platform = scope === "platform" && session.serverAdmin === true;
	const org = useGetUsageQuery({ ...range, group_by: groupBy }, { skip: platform });
	const admin = useGetAdminUsageQuery(range, { skip: !platform });
	const preset = presetOf(range);

	return (
		<div className={cn("mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-6", !embedded && "p-4 sm:p-6")} data-testid="usage-view">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			<div className="flex flex-wrap items-end gap-3" data-testid="usage-controls">
				<Segmented
					label={tl("usage.period")}
					value={preset ?? "custom"}
					options={[
						...USAGE_PRESETS.map((p) => ({ value: p, label: tl(`usage.presets.${p}`) })),
						{ value: "custom", label: tl("usage.presets.custom") },
					]}
					onChange={(v) => v !== "custom" && setRange(() => presetRange(v as UsagePreset))}
					testId="usage-preset"
				/>
				<label className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground">{t("from")}</span>
					<Input
						type="date"
						value={range.from}
						max={range.to}
						onChange={(e) => e.target.value && setRange((r) => ({ ...r, from: e.target.value }))}
						data-testid="usage-from"
					/>
				</label>
				<label className="flex flex-col gap-1 text-xs">
					<span className="text-muted-foreground">{t("to")}</span>
					<Input
						type="date"
						value={range.to}
						min={range.from}
						onChange={(e) => e.target.value && setRange((r) => ({ ...r, to: e.target.value }))}
						data-testid="usage-to"
					/>
				</label>
				{!platform && (
					<Segmented
						label={t("groupBy")}
						value={groupBy}
						options={GROUPS.map((g) => ({ value: g, label: t(`group.${g}`) }))}
						onChange={(v) => setGroupBy(v as UsageGroupBy)}
						testId="usage-group"
					/>
				)}
				{session.serverAdmin === true && (
					<Segmented
						label=""
						value={scope}
						options={[
							{ value: "org", label: t("scope.org") },
							{ value: "platform", label: t("scope.platform") },
						]}
						onChange={(v) => setScope(v as UsageScope)}
						testId="usage-scope"
					/>
				)}
				{!platform && org.data && (
					<Button
						type="button"
						size="sm"
						variant="outline"
						className="h-8 gap-1.5"
						onClick={() => org.data && downloadCsv(usageCsvName(org.data), usageCsv(org.data, tl("usage.definition")))}
						dataTestId="usage-csv"
					>
						<Download className="size-4" aria-hidden="true" />
						{tl("usage.csv")}
					</Button>
				)}
			</div>
			<UsageLinkNotes ignored={ignored} swapped={swapped} />
			{platform ? (
				<AdminUsageBody data={admin.data} loading={admin.isLoading} error={admin.error} onRetry={() => void admin.refetch()} />
			) : (
				<UsageBody data={org.data} loading={org.isLoading} error={org.error} onRetry={() => void org.refetch()} />
			)}
			{!platform && session.authMode !== "local" && <BillingUsageSection from={range.from} to={range.to} />}
		</div>
	);
}

function Segmented({
	label,
	value,
	options,
	onChange,
	testId,
}: {
	label: string;
	value: string;
	options: { value: string; label: string }[];
	onChange(v: string): void;
	testId: string;
}) {
	return (
		<div className="flex flex-col gap-1 text-xs">
			{label && <span className="text-muted-foreground">{label}</span>}
			<div
				role="radiogroup"
				aria-label={label || undefined}
				className="border-border bg-muted/50 flex rounded-lg border p-0.5"
				data-testid={testId}
			>
				{options.map((o) => (
					<button
						key={o.value}
						type="button"
						role="radio"
						aria-checked={o.value === value}
						onClick={() => onChange(o.value)}
						className={cn(
							"h-8 rounded-md px-3 text-xs font-medium transition-colors",
							o.value === value ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
						)}
						data-testid={`${testId}-${o.value}`}
					>
						{o.label}
					</button>
				))}
			</div>
		</div>
	);
}

export interface UsageBodyProps {
	data?: UsageReport;
	loading?: boolean;
	error?: unknown;
	onRetry?: () => void;
}

/** The org report: totals, chart, table and provider keys. Pure, so it renders in tests. */
export function UsageBody({ data, loading, error, onRetry }: UsageBodyProps) {
	const { t } = useTranslation("usage");
	const { t: tl } = useTranslation("uf8a");
	if (error) {
		return (
			<EmptyState
				message={t("error", { message: getBenchErrorMessage(error) })}
				action={onRetry ? { label: t("retry"), onClick: onRetry } : undefined}
				testId="usage-error"
			/>
		);
	}
	if (loading || !data) {
		return (
			<div className="flex flex-col gap-4" data-testid="usage-loading">
				<div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
					{[0, 1, 2, 3].map((i) => (
						<div key={i} className="bg-muted h-28 animate-pulse rounded-xl" />
					))}
				</div>
				<div className="bg-muted h-60 animate-pulse rounded-xl" />
			</div>
		);
	}
	const group = t(`group.${data.group_by}`).toLowerCase();
	const hasRuns = data.runs > 0;
	return (
		<div className="flex min-w-0 flex-col gap-6">
			<div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4" data-testid="usage-totals">
				<UsageTile
					label={tl("usage.cost", { from: data.from, to: data.to })}
					icon={Coins}
					value={formatUSD(data.total_cost_usd)}
					hint={tl("usage.costHint")}
					valueTestId="usage-total-cost"
				/>
				<UsageTile label={t("totals.tokens")} icon={Hash} value={formatCount(data.total_tokens)} valueTestId="usage-total-tokens" />
				<UsageTile label={t("totals.runs")} icon={PlayCircle} value={formatCount(data.runs)} valueTestId="usage-total-runs" />
				<UsageTile
					label={tl("usage.keys")}
					icon={KeyRound}
					value={formatUSD(data.keys_month_spent_usd)}
					hint={tl("usage.keysHint")}
					valueTestId="usage-total-keys"
				/>
			</div>
			<p className="text-muted-foreground max-w-prose text-xs" data-testid="usage-definition">
				{tl("usage.definition")}
			</p>
			{data.unknown_cost_runs > 0 && (
				<p className="text-muted-foreground text-xs" data-testid="usage-unknown-note">
					{t("totals.unknown", { count: data.unknown_cost_runs })}
				</p>
			)}
			{data.note && (
				<p className="text-muted-foreground text-xs" data-testid="usage-note">
					{data.note}
				</p>
			)}
			{!hasRuns && data.group_by !== "key" ? (
				<EmptyState
					message={t("empty", { from: data.from, to: data.to })}
					action={{ label: t("emptyAction"), to: "/experiments" }}
					testId="usage-empty"
				/>
			) : (
				<>
					<UsageChart buckets={data.buckets} group={group} total={data.total_cost_usd} />
					<UsageTable buckets={data.buckets} group={group} groupBy={data.group_by} />
				</>
			)}
			<KeysCard data={data} />
		</div>
	);
}

/** A totals tile whose label and hint wrap: a cut label hid which number this is. */
function UsageTile({
	label,
	value,
	icon: Icon,
	hint,
	valueTestId,
}: {
	label: string;
	value: ReactNode;
	icon: LucideIcon;
	hint?: string;
	valueTestId?: string;
}) {
	return (
		<div className="bg-card flex min-w-0 flex-col gap-2 rounded-xl border p-5">
			<div className="text-muted-foreground flex items-start justify-between gap-2 text-sm">
				<span className="min-w-0 break-words">{label}</span>
				<span className="bg-muted flex size-8 shrink-0 items-center justify-center rounded-lg">
					<Icon className="size-4" aria-hidden="true" />
				</span>
			</div>
			<div className="text-foreground truncate text-2xl font-semibold tracking-tight tabular-nums" data-testid={valueTestId}>
				{value}
			</div>
			{hint && <div className="text-muted-foreground text-xs">{hint}</div>}
		</div>
	);
}

/** Bars of cost per bucket in hand-drawn SVG, so it also renders server side (recharts needs a DOM to measure). */
export function UsageChart({ buckets, group, total }: { buckets: UsageBucket[]; group: string; total: number }) {
	const { t } = useTranslation("usage");
	const titleId = useId();
	const shown = buckets.slice(0, CHART_MAX_BARS);
	const max = Math.max(...shown.map((b) => b.cost_usd), 0);
	const width = 720;
	const height = 220;
	const pad = { left: 60, right: 12, top: 12, bottom: 30 };
	const plotW = width - pad.left - pad.right;
	const plotH = height - pad.top - pad.bottom;
	const slot = shown.length > 0 ? plotW / shown.length : plotW;
	const barW = Math.max(3, Math.min(36, slot * 0.64));
	const labelEvery = Math.max(1, Math.ceil(shown.length / 8));
	const ticks = [0, 0.25, 0.5, 0.75, 1];
	return (
		<figure className="bg-card min-w-0 rounded-xl border p-5" data-testid="usage-chart">
			<figcaption className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
				<span className="text-foreground text-base font-semibold">{t("chart.title")}</span>
				<span className="text-muted-foreground text-sm tabular-nums">{formatUSD(total)}</span>
			</figcaption>
			<div className="overflow-x-auto">
				<svg role="img" aria-labelledby={titleId} viewBox={`0 0 ${width} ${height}`} className="h-auto w-full">
					<title id={titleId}>{t("chart.summary", { group, count: buckets.length, total: formatUSD(total) })}</title>
					<g aria-hidden="true" className="text-muted-foreground" fontSize={11}>
						{ticks.map((f) => {
							const y = pad.top + plotH - f * plotH;
							return (
								<g key={f}>
									<line x1={pad.left} x2={width - pad.right} y1={y} y2={y} stroke="currentColor" strokeOpacity={f === 0 ? 0.35 : 0.12} />
									{(f === 0 || f === 0.5 || f === 1) && (
										<text x={pad.left - 8} y={y + 4} textAnchor="end" fill="currentColor">
											{f === 0 ? "$0" : formatUSD(max * f)}
										</text>
									)}
								</g>
							);
						})}
					</g>
					{shown.map((b, i) => {
						const h = max > 0 ? (b.cost_usd / max) * plotH : 0;
						const x = pad.left + i * slot + (slot - barW) / 2;
						return (
							<g key={b.key} className="group">
								<rect
									x={pad.left + i * slot}
									y={pad.top}
									width={slot}
									height={plotH}
									fill="currentColor"
									className="text-accent opacity-0 group-hover:opacity-60"
								/>
								<rect
									x={x}
									y={pad.top + plotH - h}
									width={barW}
									height={Math.max(h, 0)}
									rx={Math.min(4, barW / 3)}
									fill="var(--color-chart-cat-1)"
									data-testid={`usage-bar-${b.key}`}
								>
									<title>{`${b.label || b.key}: ${formatUSD(b.cost_usd)}`}</title>
								</rect>
								{i % labelEvery === 0 && (
									<text
										x={x + barW / 2}
										y={height - 10}
										textAnchor="middle"
										fontSize={10}
										className="text-muted-foreground"
										fill="currentColor"
										aria-hidden="true"
									>
										{shortLabel(b.key)}
									</text>
								)}
							</g>
						);
					})}
				</svg>
			</div>
		</figure>
	);
}

function shortLabel(key: string): string {
	if (/^\d{4}-\d{2}-\d{2}$/.test(key)) return key.slice(5);
	return key.length > 14 ? `${key.slice(0, 13)}...` : key;
}

function UsageTable({ buckets, group, groupBy }: { buckets: UsageBucket[]; group: string; groupBy: UsageGroupBy }) {
	const { t } = useTranslation("usage");
	const { t: tl, i18n } = useTranslation("uf8a");
	const [showEmpty, setShowEmpty] = useState(false);
	const rows = tableRows(buckets, groupBy, showEmpty);
	const emptyCount = buckets.length - tableRows(buckets, groupBy, false).length;
	return (
		<div className="flex min-w-0 flex-col gap-2">
			<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
				<Table aria-label={t("table.label", { group })} data-testid="usage-table">
					<TableHeader>
						<TableRow>
							<TableHead>{t("table.group")}</TableHead>
							<TableHead className="text-right">{t("table.cost")}</TableHead>
							{groupBy !== "key" && (
								<>
									<TableHead className="hidden text-right sm:table-cell">{t("table.tokens")}</TableHead>
									<TableHead className="text-right">{t("table.runs")}</TableHead>
									<TableHead className="text-right">{t("table.unknown")}</TableHead>
								</>
							)}
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.map((b) => (
							<TableRow key={b.key} data-testid={`usage-row-${b.key}`}>
								<TableCell className={cn("text-xs", !DAY_RE.test(b.key) && "font-mono")} title={b.key}>
									{bucketLabel(b.key, i18n.language)}
									{b.label && <span className="text-muted-foreground block font-sans">{b.label}</span>}
									{!!b.no_model_call_runs && (
										<span className="text-muted-foreground block font-sans text-xs" data-testid={`usage-no-call-${b.key}`}>
											{t("table.noModelCall", { count: b.no_model_call_runs })}
										</span>
									)}
								</TableCell>
								<TableCell className="text-right tabular-nums">{formatUSD(b.cost_usd)}</TableCell>
								{groupBy !== "key" && (
									<>
										<TableCell className="hidden text-right tabular-nums sm:table-cell">{formatCount(b.tokens)}</TableCell>
										<TableCell className="text-right tabular-nums">{b.runs}</TableCell>
										<TableCell className="text-right tabular-nums">{b.unknown_cost_runs || ""}</TableCell>
									</>
								)}
							</TableRow>
						))}
					</TableBody>
				</Table>
			</div>
			{emptyCount > 0 && (
				<div>
					<Button type="button" size="sm" variant="ghost" onClick={() => setShowEmpty((v) => !v)} dataTestId="usage-show-empty">
						{showEmpty ? tl("usage.hideEmpty") : tl("usage.showEmpty", { count: emptyCount })}
					</Button>
				</div>
			)}
		</div>
	);
}

function KeysCard({ data }: { data: UsageReport }) {
	const { t } = useTranslation("usage");
	const { t: tl } = useTranslation("uf8a");
	return (
		<Card className="rounded-xl" data-testid="usage-keys">
			<CardHeader>
				<CardTitle className="flex items-center gap-2 text-base">
					<KeyRound className="text-muted-foreground size-4" aria-hidden="true" />
					{t("keys.title")}
				</CardTitle>
			</CardHeader>
			<CardContent>
				{data.keys.length === 0 ? (
					<EmptyState
						compact
						message={t("keys.empty")}
						action={{ label: t("keys.add"), to: "/settings", search: { tab: "keys" } }}
						testId="usage-keys-empty"
					/>
				) : (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{t("keys.label")}</TableHead>
								<TableHead>{t("keys.provider")}</TableHead>
								<TableHead className="text-right">{tl("usage.keySpent")}</TableHead>
								<TableHead className="text-right">{t("keys.budget")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{data.keys.map((k) => {
								const over = k.budget_usd > 0 && k.month_spent_usd >= k.budget_usd;
								return (
									<TableRow key={k.id} data-testid={`usage-key-${k.id}`}>
										<TableCell>
											{k.label || k.id}
											{k.last4 && <span className="text-muted-foreground ml-2 font-mono text-xs">...{k.last4}</span>}
										</TableCell>
										<TableCell>{k.provider}</TableCell>
										<TableCell className={cn("text-right tabular-nums", over && "text-destructive font-medium")}>
											{formatUSD(k.month_spent_usd)}
										</TableCell>
										<TableCell className="text-right tabular-nums">
											{k.budget_usd > 0 ? formatUSD(k.budget_usd) : t("keys.noBudget")}
										</TableCell>
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
				)}
			</CardContent>
		</Card>
	);
}

export interface AdminUsageBodyProps {
	data?: AdminUsageReport;
	loading?: boolean;
	error?: unknown;
	onRetry?: () => void;
}

/** Platform usage per org (server admins). L23's admin page can embed it too. */
export function AdminUsageBody({ data, loading, error, onRetry }: AdminUsageBodyProps) {
	const { t } = useTranslation("usage");
	if (error) {
		const msg = errorStatus(error) === 403 ? t("admin.forbidden") : t("error", { message: getBenchErrorMessage(error) });
		return (
			<EmptyState
				message={msg}
				action={errorStatus(error) !== 403 && onRetry ? { label: t("retry"), onClick: onRetry } : undefined}
				testId="admin-usage-error"
			/>
		);
	}
	if (loading || !data) {
		return <div className="bg-muted h-48 animate-pulse rounded-lg" data-testid="admin-usage-loading" />;
	}
	return (
		<div className="flex min-w-0 flex-col gap-5" data-testid="admin-usage">
			<div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
				<StatCard label={t("totals.cost")} icon={Coins} value={formatUSD(data.total_cost_usd)} valueTestId="admin-usage-total-cost" />
				<StatCard label={t("totals.tokens")} icon={Hash} value={formatCount(data.total_tokens)} valueTestId="admin-usage-total-tokens" />
				<StatCard label={t("totals.runs")} icon={PlayCircle} value={formatCount(data.runs)} valueTestId="admin-usage-total-runs" />
			</div>
			<h2 className="text-base font-semibold">{t("admin.title")}</h2>
			{data.orgs.length === 0 ? (
				<EmptyState message={t("admin.empty")} testId="admin-usage-empty" />
			) : (
				<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
					<Table data-testid="admin-usage-table">
						<TableHeader>
							<TableRow>
								<TableHead>{t("admin.org")}</TableHead>
								<TableHead className="text-right">{t("table.cost")}</TableHead>
								<TableHead className="text-right">{t("table.tokens")}</TableHead>
								<TableHead className="text-right">{t("table.runs")}</TableHead>
								<TableHead className="text-right">{t("totals.keys")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{data.orgs.map((o) => (
								<TableRow key={o.org_id} data-testid={`admin-usage-row-${o.slug}`}>
									<TableCell>
										{o.name || o.slug}
										<span className="text-muted-foreground ml-2 font-mono text-xs">{o.slug}</span>
										{o.error && <div className="text-destructive text-xs">{t("admin.orgError", { error: o.error })}</div>}
									</TableCell>
									<TableCell className="text-right tabular-nums">{formatUSD(o.cost_usd)}</TableCell>
									<TableCell className="text-right tabular-nums">{formatCount(o.tokens)}</TableCell>
									<TableCell className="text-right tabular-nums">{o.runs}</TableCell>
									<TableCell className="text-right tabular-nums">{formatUSD(o.keys_month_spent_usd)}</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}