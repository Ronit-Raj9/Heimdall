import { formatBytes, formatWhole } from "@/components/billing/billing.utils";
import { LoadError } from "@/components/runs/screenStates";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useGetBillingUsageQuery } from "@/lib/store/apis/billingApi";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/**
 * Usage tab, hosted only: what the org used of Heimdall itself per day (shared worker minutes,
 * runs, evaluations, storage, judge calls on Heimdall's keys), newest day first, days with nothing
 * left out.
 */
export function BillingUsageSection({ from, to }: { from: string; to: string }) {
	const { t } = useTranslation("billing");
	const usage = useGetBillingUsageQuery({ from, to });
	if (usage.isLoading) return null;
	if (usage.isError || !usage.data) {
		return <LoadError title={t("usage.error")} error={usage.error} onRetry={() => void usage.refetch()} testId="billing-usage-error" />;
	}
	const rep = usage.data;
	const days = rep.days.filter((d) => d.shared_worker_minutes > 0 || d.runs > 0 || d.evaluations > 0 || d.operator_judge_calls > 0).reverse();
	return (
		<Card data-testid="billing-usage">
			<CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
				<div className="flex max-w-2xl flex-col gap-1">
					<CardTitle>{t("usage.title")}</CardTitle>
					<p className="text-muted-foreground text-[13px]">{t("usage.description")}</p>
				</div>
				<Link to="/settings" search={{ tab: "billing" }} className="text-primary text-sm underline-offset-2 hover:underline" data-testid="billing-usage-plan">
					{t("usage.plan")}
				</Link>
			</CardHeader>
			<CardContent className="flex flex-col gap-4">
				<dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4" data-testid="billing-usage-totals">
					<Total label={t("usage.minutes")} value={formatWhole(rep.totals.shared_worker_minutes)} />
					<Total label={t("usage.runs")} value={formatWhole(rep.totals.runs)} />
					<Total label={t("usage.evaluations")} value={formatWhole(rep.totals.evaluations)} />
					<Total label={t("usage.storageNow")} value={rep.storage_bytes === undefined ? "-" : formatBytes(rep.storage_bytes)} />
				</dl>
				{rep.unavailable && rep.unavailable.length > 0 && (
					<p className="text-muted-foreground text-xs" data-testid="billing-usage-unavailable">
						{t("usage.unavailable", { list: rep.unavailable.join(", ") })}
					</p>
				)}
				{days.length > 0 && (
					<div className="overflow-x-auto rounded-lg border">
						<Table aria-label={t("usage.title")}>
							<TableHeader>
								<TableRow>
									<TableHead>{t("usage.day")}</TableHead>
									<TableHead className="text-right">{t("usage.minutes")}</TableHead>
									<TableHead className="text-right">{t("usage.runs")}</TableHead>
									<TableHead className="text-right">{t("usage.evaluations")}</TableHead>
									<TableHead className="text-right">{t("usage.storage")}</TableHead>
									<TableHead className="text-right">{t("usage.judge")}</TableHead>
								</TableRow>
							</TableHeader>
							<TableBody>
								{days.map((d) => (
									<TableRow key={d.day} data-testid="billing-usage-row">
										<TableCell className="tabular-nums">{d.day}</TableCell>
										<TableCell className="text-right tabular-nums">{formatWhole(d.shared_worker_minutes)}</TableCell>
										<TableCell className="text-right tabular-nums">{formatWhole(d.runs)}</TableCell>
										<TableCell className="text-right tabular-nums">{formatWhole(d.evaluations)}</TableCell>
										<TableCell className="text-right tabular-nums">{d.storage_bytes === undefined ? "-" : formatBytes(d.storage_bytes)}</TableCell>
										<TableCell className="text-right tabular-nums">{formatWhole(d.operator_judge_calls)}</TableCell>
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				)}
			</CardContent>
		</Card>
	);
}

function Total({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex flex-col gap-0.5">
			<dt className="text-muted-foreground text-xs">{label}</dt>
			<dd className="text-foreground text-lg font-semibold tabular-nums">{value}</dd>
		</div>
	);
}
