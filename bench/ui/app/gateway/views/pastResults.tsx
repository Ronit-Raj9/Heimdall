import { RESULT_TONE } from "@/components/gateway/gatewayEffectCard";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useGetGatewayEffectsQuery } from "@/lib/store/apis/gatewayPromptsApi";
import type { GwEffect } from "@/lib/types/gateway";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import "./ufl1I18n";
import "./w4p6I18n";

/** The day of a computed_at time, or "" when it is not a date. Pure. */
export function effectDate(iso: string): string {
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) || d.getFullYear() < 2000 ? "" : d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

/** The table of saved feature results. Pure. */
export function PastResultsTable({ effects, names }: { effects: GwEffect[]; names: Record<string, string> }) {
	const { t } = useTranslation("ufl1");
	const { t: w } = useTranslation("w4p6");
	return (
		<div className="overflow-x-auto">
			<table className="w-full text-sm" data-testid="gateway-past-table">
				<thead className="text-muted-foreground text-left text-xs">
					<tr>
						<th className="py-1 pr-3 font-normal">{t("past.feature")}</th>
						<th className="hidden py-1 pr-3 font-normal sm:table-cell">{t("past.comparison")}</th>
						<th className="py-1 pr-3 font-normal">{t("past.effect")}</th>
						<th className="hidden py-1 font-normal whitespace-nowrap sm:table-cell">{t("past.date")}</th>
					</tr>
				</thead>
				<tbody>
					{effects.map((e) => (
						<tr key={`${e.exp_id}:${e.feature}`} className="border-t align-top" data-testid={`gateway-past-${e.exp_id}`}>
							<td className="py-1 pr-3 sm:whitespace-nowrap">{names[e.feature] ?? e.feature}</td>
							<td className="hidden max-w-56 py-1 pr-3 sm:table-cell">
								<Link to="/experiments/$expId" params={{ expId: e.exp_id }} title={e.exp_id} className="text-primary block truncate hover:underline">
									{e.exp_id}
								</Link>
							</td>
							<td className="py-1 pr-3">
								{e.result && <span className={cn("mr-2 font-medium", RESULT_TONE[e.result])}>{w(`line.result.${e.result}`)}</span>}
								{e.headline}
							</td>
							<td className="hidden py-1 whitespace-nowrap sm:table-cell">{effectDate(e.computed_at)}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

/** "Past results" on the Gateway page: GET /gateway/effects. */
export function PastResults({ names }: { names: Record<string, string> }) {
	const { t } = useTranslation("ufl1");
	const q = useGetGatewayEffectsQuery({ limit: 20 });
	return (
		<section className="flex flex-col gap-2" aria-labelledby="gateway-past-title" data-testid="gateway-past">
			<h2 id="gateway-past-title" className="text-base font-medium">
				{t("past.title")}
			</h2>
			{q.isLoading ? (
				<BlockSkeleton rows={3} testId="gateway-past-loading" />
			) : q.error ? (
				<LoadError title={t("past.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="gateway-past-error" />
			) : (q.data?.effects ?? []).length === 0 ? (
				<p className="text-muted-foreground text-sm">{t("past.none")}</p>
			) : (
				<PastResultsTable effects={q.data?.effects ?? []} names={names} />
			)}
		</section>
	);
}
