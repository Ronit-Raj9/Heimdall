import "@/app/experiments_/new/views/builderI18n";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import type { ExperimentPlan } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle, CircleCheck, OctagonX } from "lucide-react";
import { useTranslation } from "react-i18next";

/** Nothing can start while the plan has errors (identical arms, overlapping arm and probe files). */
export function planBlocksStart(plan: Pick<ExperimentPlan, "errors">): boolean {
	return plan.errors.length > 0;
}

/**
 * POST /experiments/plan, shown before anything runs: blocking errors first, then warnings,
 * sizes and the minimal detectable effect with its assumed-SD note. The plan never knows time or cost
 * (no run has happened), so it points at the Estimate instead of printing unknowns (N41).
 * estimateShown: the page shows an estimate of this same experiment.
 */
export function PlanResult({
	plan,
	className,
	estimateShown = false,
}: {
	plan: ExperimentPlan;
	className?: string;
	estimateShown?: boolean;
}) {
	const { t } = useTranslation("bench");
	const { t: tb } = useTranslation("builder");
	const locale = getLocale();
	const pp = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(plan.mde);
	const sd = new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(plan.sd);
	const blocked = planBlocksStart(plan);

	return (
		<div className={cn("flex flex-col gap-4", className)} data-testid="plan-result" data-blocked={blocked ? "true" : "false"}>
			{blocked && (
				<div
					role="alert"
					className="border-destructive/50 bg-destructive/5 flex flex-col gap-1.5 rounded-sm border px-3 py-2.5"
					data-testid="plan-errors"
				>
					<span className="text-chart-error-ink flex items-center gap-1.5 text-xs font-semibold">
						<OctagonX className="size-3.5" aria-hidden="true" />
						{t("experiments.new.plan.errors")}
					</span>
					<ul className="flex flex-col gap-1 text-sm">
						{plan.errors.map((error) => (
							<li key={error} data-testid="plan-error" className="break-words">
								{error}
							</li>
						))}
					</ul>
				</div>
			)}
			{plan.warnings.length > 0 && (
				<div
					className="border-chart-warning/60 bg-chart-warning/10 flex flex-col gap-1.5 rounded-sm border px-3 py-2.5"
					data-testid="plan-warnings"
				>
					<span className="text-chart-warning-ink flex items-center gap-1.5 text-xs font-semibold">
						<AlertTriangle className="size-3.5" aria-hidden="true" />
						{t("experiments.new.plan.warnings")}
					</span>
					<ul className="flex flex-col gap-1 text-sm">
						{plan.warnings.map((warning) => (
							<li key={warning} data-testid="plan-warning" className="break-words">
								{warning}
							</li>
						))}
					</ul>
				</div>
			)}
			{!blocked && plan.warnings.length === 0 && (
				<p className="text-chart-success-ink flex items-center gap-1.5 text-xs font-medium" data-testid="plan-no-issues">
					<CircleCheck className="size-3.5" aria-hidden="true" />
					{t("experiments.new.plan.noIssues")}
				</p>
			)}

			<dl className="grid grid-cols-2 gap-2">
				<Stat label={t("experiments.new.plan.slots")} testId="plan-slots">
					<span className="tabular-nums">{plan.slots}</span>
				</Stat>
				<Stat label={t("experiments.new.plan.pairs")} testId="plan-pairs">
					<span className="tabular-nums">{plan.pairs}</span>
				</Stat>
				<Stat label={t("experiments.new.plan.mde")} testId="plan-mde" className="col-span-2">
					<span className="tabular-nums">{t("experiments.new.plan.mdeValue", { value: pp })}</span>
					<span
						className={cn("mt-0.5 block text-xs font-normal", plan.mde_assumed_sd ? "text-chart-warning-ink" : "text-muted-foreground")}
						data-testid={plan.mde_assumed_sd ? "plan-mde-assumed-sd" : "plan-mde-sd"}
					>
						{plan.mde_assumed_sd ? t("experiments.new.plan.mdeAssumedSd", { sd }) : t("experiments.new.plan.mdeSd", { sd })}
					</span>
				</Stat>
			</dl>
			<p className="text-muted-foreground text-xs" data-testid="plan-time-cost">
				{estimateShown ? tb("plan.timeCostSee") : tb("plan.timeCostPress")}
			</p>

			{plan.conditions.length > 0 && (
				<div className="flex flex-col gap-1.5">
					<span className="text-muted-foreground text-xs font-medium">{t("experiments.new.plan.perCondition")}</span>
					<Table className="text-xs" data-testid="plan-conditions">
						<TableHeader>
							<TableRow>
								<TableHead className="h-8 px-2">{t("experiments.new.plan.condition")}</TableHead>
								<TableHead className="h-8 px-2 text-right">{t("experiments.new.plan.tasks")}</TableHead>
								<TableHead className="h-8 px-2 text-right">{t("experiments.new.plan.trials")}</TableHead>
								<TableHead className="h-8 px-2 text-right">{t("experiments.new.plan.conditionSlots")}</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{plan.conditions.map((c) => (
								<TableRow key={c.condition} data-testid={`plan-condition-${c.condition}`}>
									<TableCell className="px-2 font-mono">{c.condition}</TableCell>
									<TableCell className="px-2 text-right tabular-nums">{c.tasks}</TableCell>
									<TableCell className="px-2 text-right tabular-nums">{c.trials}</TableCell>
									<TableCell className="px-2 text-right tabular-nums">{c.slots}</TableCell>
								</TableRow>
							))}
						</TableBody>
					</Table>
				</div>
			)}
		</div>
	);
}

function Stat({ label, children, testId, className }: { label: string; children: React.ReactNode; testId: string; className?: string }) {
	return (
		<div className={cn("bg-muted/40 flex flex-col gap-0.5 rounded-sm border px-3 py-2", className)} data-testid={testId}>
			<dt className="text-muted-foreground text-[11px]">{label}</dt>
			<dd className="text-sm font-semibold">{children}</dd>
		</div>
	);
}