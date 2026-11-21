import { EmptyState, PageHeader } from "@/components/shell";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetAdminUsageQuery, useGetUsageQuery } from "@/lib/store/apis/usageApi";
import { useSession } from "@/lib/session/sessionContext";
import type { AdminUsageReport, UsageBucket, UsageGroupBy, UsageReport } from "@/lib/types/usage";
import { cn } from "@/lib/utils";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

const GROUPS: UsageGroupBy[] = ["day", "model", "agent", "key"];
const CHART_MAX_BARS = 31;

/** YYYY-MM-DD of d in UTC. */
export function isoDay(d: Date): string {
	return d.toISOString().slice(0, 10);
}

/** The default range: the 30 days ending today (UTC), the server's default too. */
export function defaultRange(now = new Date()): { from: string; to: string } {
	const from = new Date(now.getTime() - 29 * 24 * 3600 * 1000);
	return { from: isoDay(from), to: isoDay(now) };
}

export function formatUSD(v: number): string {
	if (v !== 0 && Math.abs(v) < 0.01) return `$${v.toFixed(4)}`;
	return `$${v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

export function formatCount(v: number): string {
	return Math.round(v).toLocaleString("en-US");
}

/** Usage page: the org's run cost and tokens over a range, a chart and the table under it. */
export default function UsageView() {
	const { t } = useTranslation("usage");
	const session = useSession();
	const [range, setRange] = useState(defaultRange);
	const [groupBy, setGroupBy] = useState<UsageGroupBy>("day");
	const [scope, setScope] = useState<"org" | "platform">("org");
	const platform = scope === "platform" && session.serverAdmin === true;
	const org = useGetUsageQuery({ ...range, group_by: groupBy }, { skip: platform });
	const admin = useGetAdminUsageQuery(range, { skip: !platform });

	return (
		<div className="flex min-w-0 flex-col gap-5 p-4 sm:p-6" data-testid="usage-view">
			<PageHeader title={t("title")} subtitle={t("subtitle")} />
			<div className="flex flex-wrap items-end gap-3" data-testid="usage-controls">
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
						onChange={(v) => setScope(v as "org" | "platform")}
						testId="usage-scope"
					/>
				)}
			</div>
			{platform ? (
				<AdminUsageBody data={admin.data} loading={admin.isLoading} error={admin.error} onRetry={() => void admin.refetch()} />
			) : (
				<UsageBody data={org.data} loading={org.isLoading} error={org.error} onRetry={() => void org.refetch()} />
			)}
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
			<div role="radiogroup" aria-label={label || undefined} className="border-border flex rounded-md border p-0.5" data-testid={testId}>
				{options.map((o) => (
					<button
						key={o.value}
						type="button"
						role="radio"
						aria-checked={o.value === value}
						onClick={() => onChange(o.value)}
						className={cn(
							"rounded px-2.5 py-1 text-xs transition-colors",
							o.value === value ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground",
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
		return <div className="bg-muted h-48 animate-pulse rounded-lg" data-testid="usage-loading" />;
	}
	const group = t(`group.${data.group_by}`).toLowerCase();
	const hasRuns = data.runs > 0;
	return (
		<div className="flex min-w-0 flex-col gap-5">
			<div className="grid grid-cols-2 gap-3 lg:grid-cols-4" data-testid="usage-totals">
				<Stat label={t("totals.cost")} value={formatUSD(data.total_cost_usd)} testId="usage-total-cost" />
				<Stat label={t("totals.tokens")} value={formatCount(data.total_tokens)} testId="usage-total-tokens" />
				<Stat label={t("totals.runs")} value={formatCount(data.runs)} testId="usage-total-runs" />
				<Stat label={t("totals.keys")} value={formatUSD(data.keys_month_spent_usd)} testId="usage-total-keys" />
			</div>
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

function Stat({ label, value, testId }: { label: string; value: string; testId: string }) {
	return (
		<Card className="gap-1 py-3">
			<CardContent className="px-4">
				<div className="text-muted-foreground text-xs">{label}</div>
				<div className="text-lg font-semibold tabular-nums" data-testid={testId}>
					{value}
				</div>
			</CardContent>
		</Card>
	);
}

/** Bars of cost per bucket, hand-drawn in SVG like the trends chart (recharts renders nothing without a DOM). */
export function UsageChart({ buckets, group, total }: { buckets: UsageBucket[]; group: string; total: number }) {
	const { t } = useTranslation("usage");
	const titleId = useId();
	const shown = buckets.slice(0, CHART_MAX_BARS);
	const max = Math.max(...shown.map((b) => b.cost_usd), 0);
	const width = 720;
	const height = 200;
	const pad = { left: 56, right: 8, top: 8, bottom: 28 };
	const plotW = width - pad.left - pad.right;
	const plotH = height - pad.top - pad.bottom;
	const slot = shown.length > 0 ? plotW / shown.length : plotW;
	const barW = Math.max(2, Math.min(40, slot * 0.7));
	const labelEvery = Math.max(1, Math.ceil(shown.length / 8));
	return (
		<figure className="min-w-0 overflow-x-auto" data-testid="usage-chart">
			<svg role="img" aria-labelledby={titleId} viewBox={`0 0 ${width} ${height}`} className="h-auto w-full max-w-4xl min-w-[480px]">
				<title id={titleId}>{t("chart.summary", { group, count: buckets.length, total: formatUSD(total) })}</title>
				<g aria-hidden="true" className="text-muted-foreground" fontSize={11}>
					<line x1={pad.left} x2={width - pad.right} y1={pad.top + plotH} y2={pad.top + plotH} stroke="currentColor" strokeOpacity={0.4} />
					<text x={pad.left - 6} y={pad.top + 10} textAnchor="end" fill="currentColor">
						{formatUSD(max)}
					</text>
					<text x={pad.left - 6} y={pad.top + plotH} textAnchor="end" fill="currentColor">
						$0
					</text>
				</g>
				{shown.map((b, i) => {
					const h = max > 0 ? (b.cost_usd / max) * plotH : 0;
					const x = pad.left + i * slot + (slot - barW) / 2;
					return (
						<g key={b.key}>
							<rect
								x={x}
								y={pad.top + plotH - h}
								width={barW}
								height={Math.max(h, 0)}
								rx={2}
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
		</figure>
	);
}

function shortLabel(key: string): string {
	if (/^\d{4}-\d{2}-\d{2}$/.test(key)) return key.slice(5);
	return key.length > 14 ? `${key.slice(0, 13)}...` : key;
}

function UsageTable({ buckets, group, groupBy }: { buckets: UsageBucket[]; group: string; groupBy: UsageGroupBy }) {
	const { t } = useTranslation("usage");
	const rows = groupBy === "day" ? [...buckets].reverse() : buckets;
	return (
		<Table aria-label={t("table.label", { group })} data-testid="usage-table">
			<TableHeader>
				<TableRow>
					<TableHead>{t("table.group")}</TableHead>
					<TableHead className="text-right">{t("table.cost")}</TableHead>
					{groupBy !== "key" && (
						<>
							<TableHead className="text-right">{t("table.tokens")}</TableHead>
							<TableHead className="text-right">{t("table.runs")}</TableHead>
							<TableHead className="text-right">{t("table.unknown")}</TableHead>
						</>
					)}
				</TableRow>
			</TableHeader>
			<TableBody>
				{rows.map((b) => (
					<TableRow key={b.key} data-testid={`usage-row-${b.key}`}>
						<TableCell className="font-mono text-xs">
							{b.key}
							{b.label && <span className="text-muted-foreground ml-2 font-sans">{b.label}</span>}
						</TableCell>
						<TableCell className="text-right tabular-nums">{formatUSD(b.cost_usd)}</TableCell>
						{groupBy !== "key" && (
							<>
								<TableCell className="text-right tabular-nums">{formatCount(b.tokens)}</TableCell>
								<TableCell className="text-right tabular-nums">{b.runs}</TableCell>
								<TableCell className="text-right tabular-nums">{b.unknown_cost_runs || ""}</TableCell>
							</>
						)}
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function KeysCard({ data }: { data: UsageReport }) {
	const { t } = useTranslation("usage");
	return (
		<Card data-testid="usage-keys">
			<CardHeader>
				<CardTitle className="text-sm">{t("keys.title")}</CardTitle>
			</CardHeader>
			<CardContent>
				{data.keys.length === 0 ? (
					<EmptyState compact message={t("keys.empty")} action={{ label: t("keys.add"), to: "/models" }} testId="usage-keys-empty" />
				) : (
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>{t("keys.label")}</TableHead>
								<TableHead>{t("keys.provider")}</TableHead>
								<TableHead className="text-right">{t("keys.spent")}</TableHead>
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
			<div className="grid grid-cols-2 gap-3 lg:grid-cols-3">
				<Stat label={t("totals.cost")} value={formatUSD(data.total_cost_usd)} testId="admin-usage-total-cost" />
				<Stat label={t("totals.tokens")} value={formatCount(data.total_tokens)} testId="admin-usage-total-tokens" />
				<Stat label={t("totals.runs")} value={formatCount(data.runs)} testId="admin-usage-total-runs" />
			</div>
			<h2 className="text-sm font-semibold">{t("admin.title")}</h2>
			{data.orgs.length === 0 ? (
				<EmptyState message={t("admin.empty")} testId="admin-usage-empty" />
			) : (
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
			)}
		</div>
	);
}
