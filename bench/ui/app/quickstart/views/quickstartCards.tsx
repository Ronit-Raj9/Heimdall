import { INSTALL_STEP, QUICKSTART_STEPS, minedRows, reviewCommand } from "@/components/quickstart/quickstart.utils";
import "@/lib/types/u19I18n";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Check, CheckCircle2, ChevronDown, Copy, Inbox } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

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
				<span className="text-zinc-400 select-none">$ </span>
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

/**
 * The commands as numbered step cards: step 0 builds the tool, then the four workflow commands.
 * "Step n" is the anchor; the one step that calls models says it estimates the cost first (X-15).
 */
export function QuickstartSteps() {
	const { t } = useTranslation("u19");
	return (
		<ol className="grid gap-4 md:grid-cols-2" data-testid="quickstart-steps">
			{[INSTALL_STEP, ...QUICKSTART_STEPS].map((s, i) => (
				<li
					key={s.key}
					className={cn("bg-card flex min-w-0 flex-col gap-3 rounded-xl border p-5", s.key === "install" && "md:col-span-2")}
					data-testid={`quickstart-step-${s.key}`}
				>
					<div className="flex min-w-0 flex-col gap-0.5">
						<span className="text-foreground text-sm font-semibold">{t("quickstart.step", { n: i })}</span>
						<StepTitle stepKey={s.key} />
					</div>
					<p className="text-muted-foreground line-clamp-2 text-sm">{t(`quickstart.steps.${s.key}`)}</p>
					{s.key === "compare" && <p className="text-muted-foreground text-xs">{t("quickstart.costForecast")}</p>}
					<CommandWell command={s.command} testId={`quickstart-cmd-${s.key}`} />
				</li>
			))}
		</ol>
	);
}

function StepTitle({ stepKey }: { stepKey: (typeof QUICKSTART_STEPS)[number]["key"] | typeof INSTALL_STEP.key }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u19");
	if (stepKey === "install")
		return <span className="text-foreground text-base font-semibold tracking-tight">{tu("quickstart.installTitle")}</span>;
	return <span className="text-foreground text-base font-semibold tracking-tight">{t(`quickstart.step.${stepKey}.title`)}</span>;
}

/** Per suite, the mined tasks and how many drafted instructions still need a human read. */
export function MinedTasks({ suites }: { suites: readonly SuiteSummary[] }) {
	const { t } = useTranslation("u19");
	const { t: tb } = useTranslation("bench");
	const rows = minedRows(suites).filter((r) => r.mined > 0 || r.unreviewed.length > 0);
		return (
		<section className="flex flex-col gap-4" data-testid="quickstart-mined">
			<div className="flex flex-wrap items-end justify-between gap-2">
				<div className="flex flex-col gap-0.5">
					<h2 className="text-foreground text-lg font-semibold tracking-tight">{t("quickstart.minedTitle")}</h2>
					<p className="text-muted-foreground text-sm">{t("quickstart.minedSubtitle")}</p>
				</div>
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
							<div className="flex min-w-0 flex-col gap-1 sm:flex-row sm:items-center sm:gap-3">
								<span className="text-foreground min-w-0 truncate font-medium sm:flex-1" title={r.suite}>
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
