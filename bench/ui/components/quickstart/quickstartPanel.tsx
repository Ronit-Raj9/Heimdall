import { EmptyNote, Section } from "@/components/runs/screenStates";
import type { SuiteSummary } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";
import { QUICKSTART_STEPS, minedRows, reviewCommand, unreviewedTotal } from "./quickstart.utils";

/**
 * First workflow page body: the four commands that answer "will this change break my coding agent?"
 * with what each costs, and per suite the tasks `tasks mine` cut, with every task whose drafted
 * instruction nobody has reviewed and the command that marks it reviewed.
 */
export function QuickstartBody({ suites }: { suites: readonly SuiteSummary[] }) {
	const { t } = useTranslation("bench");
	const rows = minedRows(suites);
	const total = unreviewedTotal(rows);
	const anyMined = rows.some((r) => r.mined > 0);
	return (
		<div className="flex flex-col gap-8" data-testid="quickstart-body">
			<Section title={t("quickstart.stepsTitle")} description={t("quickstart.stepsHint")} testId="quickstart-steps">
				<ol className="flex flex-col gap-3">
					{QUICKSTART_STEPS.map((s, i) => (
						<li key={s.key} className="flex flex-col gap-1" data-testid={`quickstart-step-${s.key}`}>
							<span className="text-foreground text-sm font-medium">
								{i + 1}. {t(`quickstart.step.${s.key}.title`)}
							</span>
							<code className="bg-muted/40 rounded px-2 py-1 font-mono text-xs break-all">{s.command}</code>
							<span className="text-muted-foreground text-xs">
								{t(`quickstart.step.${s.key}.what`)} {t("quickstart.cost")}: {t(`quickstart.step.${s.key}.cost`)}
							</span>
						</li>
					))}
				</ol>
			</Section>
			<Section title={t("quickstart.minedTitle")} description={t("quickstart.minedHint")} testId="quickstart-mined">
				{!anyMined ? (
					<EmptyNote testId="quickstart-no-mined">{t("quickstart.noMined")}</EmptyNote>
				) : (
					<>
						<p className="text-sm" data-testid="quickstart-unreviewed-total">
							{t("quickstart.unreviewedTotal", { count: total })}
						</p>
						<table className="w-full text-sm" data-testid="quickstart-mined-table">
							<thead>
								<tr className="text-muted-foreground text-left text-xs">
									<th className="py-1 pr-4 font-medium">{t("quickstart.col.suite")}</th>
									<th className="py-1 pr-4 font-medium">{t("quickstart.col.mined")}</th>
									<th className="py-1 pr-4 font-medium">{t("quickstart.col.admitted")}</th>
									<th className="py-1 font-medium">{t("quickstart.col.unreviewed")}</th>
								</tr>
							</thead>
							<tbody>
								{rows
									.filter((r) => r.mined > 0 || r.unreviewed.length > 0)
									.map((r) => (
										<tr key={r.suite} className="border-t align-top" data-testid={`quickstart-row-${r.suite}`}>
											<td className="py-2 pr-4 font-mono text-xs">{r.suite}</td>
											<td className="py-2 pr-4 tabular-nums">
												{r.mined} / {r.tasks}
											</td>
											<td className="py-2 pr-4 tabular-nums">{r.admittedMined}</td>
											<td className="py-2">
												{r.unreviewed.length === 0 ? (
													<span className="text-muted-foreground text-xs">{t("quickstart.allReviewed")}</span>
												) : (
													<ul className="flex flex-col gap-1">
														{r.unreviewed.map((task) => (
															<li key={task.id} data-testid={`quickstart-unreviewed-${task.id}`}>
																<span className="text-xs">{task.title}</span>
																<code className="text-muted-foreground block font-mono text-[11px]">
																	{reviewCommand(r.suite, task.id)}
																</code>
															</li>
														))}
													</ul>
												)}
											</td>
										</tr>
									))}
							</tbody>
						</table>
					</>
				)}
			</Section>
		</div>
	);
}
