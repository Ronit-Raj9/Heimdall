import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useGetRunBifrostQuery, type BifrostCall, type RunBifrost } from "@/lib/store/apis/runBifrostApi";
import type { Tokens } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		throughBifrost: typeof import("@/locales/en/throughBifrost.json");
	}
}

/** Sum of a call's token counts; undefined when Bifrost recorded none. */
export function tokenSum(t?: Tokens): number | undefined {
	return t ? t.fresh + t.cache_read + t.cache_write + t.out : undefined;
}

/** A dollar amount with four decimals, the run page's cost format. */
export function usd(v: number): string {
	return `$${v.toFixed(4)}`;
}

/** A latency in ms, shown in seconds from one second up. */
export function ms(v: number): string {
	return v >= 1000 ? `${(v / 1000).toFixed(1)} s` : `${Math.round(v)} ms`;
}

/** Lane C09: the run page's Through Bifrost panel, fetched by run id. */
export function RunThroughBifrost({ runId, arm }: { runId: string; arm: string }) {
	const { t } = useTranslation("throughBifrost");
	const q = useGetRunBifrostQuery(runId);
	if (q.isLoading) return <BlockSkeleton rows={4} testId={`bifrost-${arm}-loading`} />;
	if (q.error || !q.data) {
		return <LoadError title={t("loadError")} error={q.error} onRetry={() => void q.refetch()} testId={`bifrost-${arm}-error`} />;
	}
	return <ThroughBifrost data={q.data} arm={arm} />;
}

export function ThroughBifrost({ data, arm }: { data: RunBifrost; arm: string }) {
	const { t } = useTranslation("throughBifrost");
	if (!data.available) return <EmptyNote testId={`bifrost-${arm}-empty`}>{t("empty")}</EmptyNote>;
	const tokens = tokenSum(data.tokens) ?? 0;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid={`bifrost-${arm}`}>
			{data.source && <p className="text-muted-foreground text-xs">{t(`source.${data.source}`)}</p>}
			{!data.complete && (
				<p className="text-xs text-amber-700 dark:text-amber-400" data-testid={`bifrost-${arm}-incomplete`}>
					{t("incomplete")}
				</p>
			)}
			<dl className="grid grid-cols-2 gap-2 sm:grid-cols-5">
				<Stat label={t("stat.calls")} value={String(data.calls)} testId={`bifrost-${arm}-calls`} />
				<Stat label={t("stat.fallbacks")} value={String(data.fallbacks)} testId={`bifrost-${arm}-fallbacks`} />
				<Stat label={t("stat.cacheHits")} value={String(data.cache_hits)} testId={`bifrost-${arm}-cache`} />
				<Stat label={t("stat.faults")} value={String(data.faults)} testId={`bifrost-${arm}-faults`} />
				<Stat label={t("stat.errors")} value={String(data.errors)} testId={`bifrost-${arm}-errors`} />
				<Stat label={t("stat.tokens")} value={tokens.toLocaleString("en-US")} testId={`bifrost-${arm}-tokens`} />
				<Stat
					label={t("stat.cost")}
					value={data.cost_usd != null ? usd(data.cost_usd) : t("costUnknown")}
					note={data.cost_usd != null && data.cost_known < data.calls ? t("costPartial", { known: data.cost_known, calls: data.calls }) : undefined}
					testId={`bifrost-${arm}-cost`}
				/>
				<Stat label={t("stat.latency")} value={ms(data.avg_latency_ms)} testId={`bifrost-${arm}-latency`} />
				<Stat label={t("stat.providers")} value={data.providers.join(", ") || t("none")} testId={`bifrost-${arm}-providers`} />
				<Stat label={t("stat.models")} value={data.models.join(", ") || t("none")} testId={`bifrost-${arm}-models`} />
			</dl>
			<Table data-testid={`bifrost-${arm}-calls-table`}>
				<TableHeader>
					<TableRow>
						<TableHead>{t("col.n")}</TableHead>
						<TableHead>{t("col.model")}</TableHead>
						<TableHead>{t("col.status")}</TableHead>
						<TableHead>{t("col.fallback")}</TableHead>
						<TableHead>{t("col.cache")}</TableHead>
						<TableHead>{t("col.fault")}</TableHead>
						<TableHead className="text-right">{t("col.tokens")}</TableHead>
						<TableHead className="text-right">{t("col.cost")}</TableHead>
						<TableHead className="text-right">{t("col.latency")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{data.call_list.map((c, i) => (
						<CallRow key={`${c.id}-${i}`} call={c} n={i + 1} />
					))}
				</TableBody>
			</Table>
		</div>
	);
}

function CallRow({ call, n }: { call: BifrostCall; n: number }) {
	const { t } = useTranslation("throughBifrost");
	const tokens = tokenSum(call.tokens);
	const failed = call.status === "error";
	return (
		<TableRow data-testid={`bifrost-call-${n}`}>
			<TableCell className="tabular-nums">{n}</TableCell>
			<TableCell>
				<span className="font-mono text-xs">
					{call.provider}/{call.model_served}
				</span>
				{call.model_requested && call.model_requested !== call.model_served && (
					<span className="text-muted-foreground block text-xs">{t("requested", { model: call.model_requested })}</span>
				)}
			</TableCell>
			<TableCell className={failed ? "text-destructive" : undefined}>
				{failed ? t("failed", { status: call.error_status ?? "" }).trim() : t("ok")}
			</TableCell>
			<TableCell>{call.fallback_index > 0 ? t("fallbackN", { n: call.fallback_index }) : t("no")}</TableCell>
			<TableCell>{call.cache_hit ? t("hit") : t("no")}</TableCell>
			<TableCell>{call.fault || t("none")}</TableCell>
			<TableCell className="text-right tabular-nums">{tokens != null ? tokens.toLocaleString("en-US") : "-"}</TableCell>
			<TableCell className="text-right tabular-nums">{call.cost_usd != null ? usd(call.cost_usd) : "-"}</TableCell>
			<TableCell className="text-right tabular-nums">{ms(call.latency_ms)}</TableCell>
		</TableRow>
	);
}

function Stat({ label, value, note, testId }: { label: string; value: string; note?: string; testId: string }) {
	return (
		<div className="rounded-sm border px-3 py-2" data-testid={testId}>
			<dt className="text-muted-foreground text-xs">{label}</dt>
			<dd className="truncate text-sm font-medium tabular-nums">{value}</dd>
			{note && <dd className="text-muted-foreground text-xs">{note}</dd>}
		</div>
	);
}
