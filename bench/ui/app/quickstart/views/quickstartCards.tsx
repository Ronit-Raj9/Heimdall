import { QUICKSTART_STEPS, minedRows, reviewCommand, unreviewedTotal } from "@/components/quickstart/quickstart.utils";
import "@/lib/types/u19I18n";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Check, CheckCircle2, ChevronDown, Copy, FolderCog, GitCompareArrows, GitPullRequest, Inbox, Pickaxe } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

const STEP_ICONS = { init: FolderCog, mine: Pickaxe, compare: GitCompareArrows, ci: GitPullRequest } as const;

/** A command in a dark well with a copy button. */
function CommandWell({ command, testId }: { command: string; testId: string }) {
	const { t } = useTranslation("u19");
	const [copied, setCopied] = useState(false);
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), 1400);
		return () => window.clearTimeout(timer);
	}, [copied]);
	const copy = () => {
		void navigator.clipboard?.writeText(command).then(() => setCopied(true));
	};
	return (
		<div className="group/cmd relative flex min-w-0 items-center rounded-lg bg-zinc-950 text-zinc-100 dark:bg-black/60" data-testid={testId}>
			<code className="min-w-0 flex-1 overflow-x-auto px-3 py-2.5 pr-10 font-mono text-xs whitespace-nowrap">
				<span className="text-zinc-500 select-none">$ </span>
				{command}
			</code>
			<button
				type="button"
				onClick={copy}
				className="absolute right-1.5 flex size-7 items-center justify-center rounded-md text-zinc-400 transition-colors hover:bg-white/10 hover:text-white focus-visible:outline-2 focus-visible:outline-white/60"
				aria-label={copied ? t("quickstart.copied") : t("quickstart.copy")}
				title={copied ? t("quickstart.copied") : t("quickstart.copy")}
			>
				{copied ? <Check className="size-3.5" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
			</button>
		</div>
	);
}

/** The four commands as numbered step cards. */
export function QuickstartSteps() {
	const { t } = useTranslation("u19");
	return (
		<ol className="grid gap-4 md:grid-cols-2" data-testid="quickstart-steps">
			{QUICKSTART_STEPS.map((s, i) => {
				const Icon = STEP_ICONS[s.key];
				const free = s.key !== "compare";
				return (
					<li
						key={s.key}
						className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5 transition-shadow hover:shadow-md"
						data-testid={`quickstart-step-${s.key}`}
					>
						<div className="flex items-start gap-3">
							<span className="bg-primary/10 text-primary flex size-10 shrink-0 items-center justify-center rounded-xl">
								<Icon className="size-5" aria-hidden="true" />
							</span>
							<div className="flex min-w-0 flex-1 flex-col gap-0.5">
								<span className="text-muted-foreground text-xs font-medium">{t("quickstart.step", { n: i + 1 })}</span>
								<StepTitle stepKey={s.key} />
							</div>
							<span
								className={cn(
									"shrink-0 rounded-full px-2 py-0.5 text-xs font-medium",
									free ? "bg-chart-success/10 text-chart-success-ink" : "bg-chart-warning/10 text-chart-warning-ink",
								)}
							>
								{free ? t("quickstart.costFree") : t("quickstart.costForecast")}
							</span>
						</div>
						<p className="text-muted-foreground line-clamp-2 text-sm">{t(`quickstart.steps.${s.key}`)}</p>
						<CommandWell command={s.command} testId={`quickstart-cmd-${s.key}`} />
					</li>
				);
			})}
		</ol>
	);
}

function StepTitle({ stepKey }: { stepKey: (typeof QUICKSTART_STEPS)[number]["key"] }) {
	const { t } = useTranslation("bench");
	return <span className="text-foreground text-base font-semibold tracking-tight">{t(`quickstart.step.${stepKey}.title`)}</span>;
}

/** Per suite, the mined tasks and how many drafted instructions still need a human read. */
export function MinedTasks({ suites }: { suites: readonly SuiteSummary[] }) {
	const { t } = useTranslation("u19");
	const { t: tb } = useTranslation("bench");
	const rows = minedRows(suites).filter((r) => r.mined > 0 || r.unreviewed.length > 0);
	const total = unreviewedTotal(rows);
	return (
		<section className="flex flex-col gap-4" data-testid="quickstart-mined">
			<div className="flex flex-wrap items-end justify-between gap-2">
				<div className="flex flex-col gap-0.5">
					<h2 className="text-foreground text-lg font-semibold tracking-tight">{t("quickstart.minedTitle")}</h2>
					<p className="text-muted-foreground text-sm">{t("quickstart.minedSubtitle")}</p>
				</div>
				{rows.length > 0 && (
					<span
						className={cn(
							"rounded-full px-2.5 py-0.5 text-xs font-medium",
							total > 0 ? "bg-chart-warning/10 text-chart-warning-ink" : "bg-chart-success/10 text-chart-success-ink",
						)}
						data-testid="quickstart-unreviewed-total"
					>
						{total > 0 ? t("quickstart.needsReview", { count: total }) : t("quickstart.allReviewed")}
					</span>
				)}
			</div>
			{rows.length === 0 ? (
				<div className="flex flex-col items-center gap-3 rounded-xl border border-dashed px-6 py-10 text-center" data-testid="quickstart-no-mined">
					<span className="bg-muted text-muted-foreground flex size-11 items-center justify-center rounded-full">
						<Inbox className="size-5" aria-hidden="true" />
					</span>
					<p className="text-muted-foreground max-w-sm text-sm">{tb("quickstart.noMined")}</p>
				</div>
			) : (
				<ul className="bg-card divide-y rounded-xl border" data-testid="quickstart-mined-table">
					{rows.map((r) => (
						<li key={r.suite} className="flex flex-col gap-2 px-5 py-4" data-testid={`quickstart-row-${r.suite}`}>
							<div className="flex min-w-0 items-center gap-3">
								<span className="text-foreground min-w-0 flex-1 truncate font-medium" title={r.suite}>
									{r.suite}
								</span>
								<span className="text-muted-foreground text-xs tabular-nums">
									{t("quickstart.minedCount", { mined: r.mined, tasks: r.tasks })}
								</span>
								{r.unreviewed.length === 0 ? (
									<span className="text-chart-success-ink flex items-center gap-1 text-xs font-medium">
										<CheckCircle2 className="size-3.5" aria-hidden="true" />
										{t("quickstart.allReviewed")}
									</span>
								) : (
									<span className="bg-chart-warning/10 text-chart-warning-ink rounded-full px-2 py-0.5 text-xs font-medium">
										{t("quickstart.needsReview", { count: r.unreviewed.length })}
									</span>
								)}
							</div>
							{r.unreviewed.length > 0 && (
								<details className="group">
									<summary className="text-muted-foreground hover:text-foreground flex w-fit cursor-pointer list-none items-center gap-1 text-xs">
										<ChevronDown className="size-3.5 transition-transform group-open:rotate-180" aria-hidden="true" />
										{t("quickstart.showCommands")}
									</summary>
									<ul className="mt-2 flex flex-col gap-2">
										{r.unreviewed.map((task) => (
											<li key={task.id} className="flex flex-col gap-1" data-testid={`quickstart-unreviewed-${task.id}`}>
												<span className="text-sm">{task.title}</span>
												<CommandWell command={reviewCommand(r.suite, task.id)} testId={`quickstart-review-${task.id}`} />
											</li>
										))}
									</ul>
								</details>
							)}
						</li>
					))}
				</ul>
			)}
		</section>
	);
}
