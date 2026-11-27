import { ValueCell } from "@/components/valueCell";
import { formatValueNumber } from "@/components/valueCell.utils";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import type { BenchEvent, Tokens } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { IncompleteNotice } from "./incompleteNotice";
import { gatewaySummary, isServedByOther, tokenTotal } from "./runs.utils";

export interface GatewayLaneProps {
	arm: string;
	events: readonly BenchEvent[];
	/** From GET /runs/{id}/events. False: some gateway rows are missing. */
	complete: boolean;
}

/**
 * Bifrost's record of one run's model calls (DESIGN.md 18 screen 2): model requested against
 * served, attempts, retries, fallbacks, tokens and cost, all from source "gateway" events.
 */
export function GatewayLane({ arm, events, complete }: GatewayLaneProps) {
	const { t } = useTranslation("bench");
	const summary = useMemo(() => gatewaySummary(events, complete), [events, complete]);

	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid={`gateway-lane-${arm}`} data-complete={complete ? "true" : "false"}>
			<div className="flex items-center justify-between gap-2">
				<h3 className="text-sm font-semibold">{t("runs.arm", { arm })}</h3>
			</div>
			{!complete && <IncompleteNotice arm={arm} />}
			{/* M4-59: a setup with no gateway calls gets the one empty line, not a row of zeros. */}
			{summary.calls.length > 0 && (
			<dl className="grid grid-cols-3 gap-2 sm:grid-cols-5">
				<Stat label={t("runs.gateway.requests")} testId={`gateway-${arm}-requests`}>
					<span className="tabular-nums">{summary.requests}</span>
				</Stat>
				<Stat label={t("runs.gateway.retries")} testId={`gateway-${arm}-retries`}>
					<span className="tabular-nums">{summary.retries}</span>
				</Stat>
				<Stat label={t("runs.gateway.fallbacks")} testId={`gateway-${arm}-fallbacks`}>
					<span className="tabular-nums">{summary.fallbacks}</span>
				</Stat>
				<Stat label={t("runs.gateway.tokens")} testId={`gateway-${arm}-tokens`}>
					<ValueCell value={summary.tokens} kind="tokens" testId={`gateway-${arm}-tokens-value`} />
				</Stat>
				<Stat label={t("runs.gateway.cost")} testId={`gateway-${arm}-cost`}>
					<ValueCell value={summary.cost} kind="usd" testId={`gateway-${arm}-cost-value`} />
				</Stat>
			</dl>
			)}
			{summary.calls.length === 0 ? (
				<p
					className="text-muted-foreground rounded-sm border border-dashed px-3 py-4 text-center text-xs"
					data-testid={`gateway-${arm}-empty`}
				>
					{t("runs.gateway.empty")}
				</p>
			) : (
				<Table className="text-xs" data-testid={`gateway-${arm}-calls`}>
					<TableHeader>
						<TableRow>
							<TableHead className="h-8 px-2">{t("runs.gateway.seq")}</TableHead>
							<TableHead className="h-8 px-2">{t("runs.gateway.type")}</TableHead>
							<TableHead className="h-8 px-2">{t("runs.gateway.requested")}</TableHead>
							<TableHead className="h-8 px-2">{t("runs.gateway.served")}</TableHead>
							<TableHead className="h-8 px-2 text-right">{t("runs.gateway.attempt")}</TableHead>
							<TableHead className="h-8 px-2 text-right">{t("runs.gateway.tokens")}</TableHead>
							<TableHead className="h-8 px-2 text-right">{t("runs.gateway.cost")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{summary.calls.map((event) => (
							<GatewayRow key={event.event_id} event={event} />
						))}
					</TableBody>
				</Table>
			)}
		</div>
	);
}

function GatewayRow({ event }: { event: BenchEvent }) {
	const { t } = useTranslation("bench");
	const switched = isServedByOther(event);
	return (
		<TableRow data-testid={`gateway-row-${event.event_id}`} className={cn(event.is_error && "bg-chart-error/5")}>
			<TableCell className="text-muted-foreground px-2 font-mono tabular-nums">{event.seq}</TableCell>
			<TableCell className="px-2 font-mono">{event.type}</TableCell>
			<TableCell className="px-2 font-mono">{event.model_requested ?? ""}</TableCell>
			<TableCell className="px-2 font-mono">
				<span
					className={cn(switched && "text-chart-warning-ink font-semibold")}
					title={switched ? t("runs.gateway.fallbackFlag") : undefined}
				>
					{event.model_served ?? ""}
				</span>
			</TableCell>
			<TableCell className="px-2 text-right tabular-nums">{event.attempt_no ?? ""}</TableCell>
			<TableCell className="px-2 text-right">{event.tokens && <TokenTotal tokens={event.tokens} />}</TableCell>
			<TableCell className="px-2 text-right">
				{event.usd && <ValueCell value={event.usd} kind="usd" testId={`gateway-cost-${event.event_id}`} />}
			</TableCell>
		</TableRow>
	);
}

function TokenTotal({ tokens }: { tokens: Tokens }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const fmt = (n: number) => formatValueNumber(n, "tokens", locale);
	const breakdown = t("runs.gateway.tokenBreakdown", {
		fresh: fmt(tokens.fresh),
		cacheRead: fmt(tokens.cache_read),
		cacheWrite: fmt(tokens.cache_write),
		out: fmt(tokens.out),
	});
	return (
		<span title={breakdown} aria-label={breakdown} className="cursor-help underline decoration-dotted underline-offset-2">
			<ValueCell value={{ state: "observed", v: tokenTotal(tokens), evidence: "gateway" }} kind="tokens" />
		</span>
	);
}

function Stat({ label, children, testId }: { label: string; children: React.ReactNode; testId: string }) {
	return (
		<div className="bg-muted/40 flex flex-col gap-0.5 rounded-sm border px-2 py-1.5" data-testid={testId}>
			<dt className="text-muted-foreground text-[11px]">{label}</dt>
			<dd className="text-sm font-medium">{children}</dd>
		</div>
	);
}