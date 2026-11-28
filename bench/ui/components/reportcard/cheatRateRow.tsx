// Cheat rate on impossible twins (W4-48, design D4 10.5): how often each arm passed a task that cannot honestly be
// passed. Shown on the report card when the server sends the cheat check row; a rate is always shown with its counts.
import "@/components/issuebench/w448I18n";
import { Section } from "@/components/ui/section";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { CappedPaidAction } from "@/components/evaluations/cappedPaidAction";
import { costPerRun, estimateOf, useStartCheatCheckCappedMutation } from "@/components/evaluations/paidCaps";
import { isBuiltinSuite } from "@/components/suites/suiteDisplay";
import { useGetSuiteQuery } from "@/lib/store/apis/suitesLightApi";
import "./uff5I18n";
import type { Report } from "@/lib/store/apis/evaluationsApi";
import { Link } from "@tanstack/react-router";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { ReactReduxContext } from "react-redux";
import "./reportCardI18n";

/** One arm's result on the impossible twins. */
export interface CheatArm {
	passed: number;
	runs: number;
	rate: number;
	low: number;
	high: number;
	pass_runs?: string[];
}

/** The report card's cheat check row (reportcard.CheatCheck). */
export interface CheatCheck {
	evaluation?: string;
	per_arm?: Record<string, CheatArm>;
	state: "measured" | "running" | "not_measured" | "no_twins";
	/** About how many runs a cheat check starts (state not_measured). */
	est_runs?: number;
}

/** Fewer twin runs than this get the warning-sign note. */
export const FEW_CHEAT_RUNS = 10;

const pct = (v: number) => `${Math.round(v * 100)}%`;

/** The cheat check of a report, or undefined when the server sent none (a shared page, an older server). */
export function cheatCheckOf(report: Report): CheatCheck | undefined {
	return (report as Report & { cheat_check?: CheatCheck }).cheat_check;
}

/** The row; it renders nothing without a cheat check, and its buttons only inside a data store. */
export function CheatRateRow({ report }: { report: Report }) {
	const cc = cheatCheckOf(report);
	if (!cc) return null;
	return <CheatRateSection report={report} cc={cc} />;
}

function CheatRateSection({ report, cc }: { report: Report; cc: CheatCheck }) {
	const { t } = useTranslation("w448");
	const { t: tr } = useTranslation("reportCard");
	const store = useContext(ReactReduxContext);
	const arms = report.arms.filter((a) => cc.per_arm?.[a.label]);
	const few = arms.some((a) => (cc.per_arm?.[a.label]?.runs ?? 0) < FEW_CHEAT_RUNS);
	return (
		<Section as="h3" title={t("cheat.title")} description={t("cheat.description")}>
			<div data-testid="report-cheat-rate" className="flex flex-col gap-2 text-sm">
				{cc.state === "measured" &&
					arms.map((a) => {
						const r = cc.per_arm![a.label];
						return (
							<div key={a.label} data-testid={`cheat-arm-${a.label}`} className="flex flex-col gap-1">
								<span className="tabular-nums">
									{t("cheat.measured", {
										arm: a.label,
										passed: r.passed,
										runs: r.runs,
										rate: pct(r.rate),
										low: pct(r.low),
										high: pct(r.high),
									})}
								</span>
								{(r.pass_runs ?? []).length > 0 && (
									<span className="flex flex-wrap gap-2">
										{(r.pass_runs ?? []).map((id, i) => (
											<Link key={id} to="/runs/$runId" params={{ runId: id }} className="underline">
												{t("cheat.passRun", { n: i + 1 })}
											</Link>
										))}
									</span>
								)}
							</div>
						);
					})}
				{cc.state === "measured" && <p className="text-muted-foreground">{t("cheat.measuredNote")}</p>}
				{cc.state === "measured" && few && <p className="text-muted-foreground">{t("cheat.fewRuns")}</p>}
				{cc.state === "running" && <p>{t("cheat.running")}</p>}
				{cc.state === "not_measured" && <p>{t("cheat.notMeasured")}</p>}
				{/* A report never creates benchmark tasks: making twins lives on the benchmark page (M3-14, area 6). */}
				{cc.state === "no_twins" && (store ? <NoTwinsNote report={report} notRun={tr("cheat.notRun")} /> : <p>{tr("cheat.notRun")}</p>)}
				{store && cc.state === "not_measured" && <CheatActions report={report} cc={cc} />}
			</div>
		</Section>
	);
}

/** Where a cheat check can be set up (F5, PH2-4): built-in benchmarks cannot get twins; an own benchmark makes them on its page. */
function NoTwinsNote({ report, notRun }: { report: Report; notRun: string }) {
	const { t } = useTranslation("uff5");
	const suite = report.benchmarks.map((b) => b.benchmark.split("@")[0]).find((b) => !b.endsWith("-twins"));
	const q = useGetSuiteQuery(suite ?? "", { skip: !suite });
	const builtin = q.data ? isBuiltinSuite(q.data) : undefined;
	return (
		<div className="flex flex-col gap-1" data-testid="cheat-no-twins">
			<p>{notRun}</p>
			{builtin === true && <p className="text-muted-foreground">{t("cheat.builtin")}</p>}
			{builtin === false && suite && (
				<p className="text-muted-foreground">
					{t("cheat.ownBefore")}{" "}
					<Link to="/suites/$suiteId" params={{ suiteId: suite }} search={{ tab: "quality" } as never} className="underline" data-testid="cheat-open-benchmark">
						{t("cheat.ownLink")}
					</Link>
				</p>
			)}
		</div>
	);
}

/** The cheat check start: its own cap (default from the estimate), the estimate and a confirm that names the amount (F5, PH2-3). */
function CheatActions({ report, cc }: { report: Report; cc: CheatCheck }) {
	const { t } = useTranslation("w448");
	const { t: tu } = useTranslation("uff5");
	const [start, started] = useStartCheatCheckCappedMutation();
	const runs = cc.est_runs && cc.est_runs > 0 ? cc.est_runs : undefined;
	const estimate = runs ? estimateOf(runs, costPerRun(report.totals)) : null;
	return (
		<div className="flex flex-col gap-1">
			<CappedPaidAction
				variant="outline"
				size="sm"
				kind="form"
				verb={t("cheat.check")}
				estimateUsd={estimate}
				estimateRuns={runs}
				confirmText={tu("cheat.confirm")}
				disabled={started.isLoading || started.isSuccess}
				isLoading={started.isLoading}
				onConfirm={async (cap) => {
					try {
						await start({ evalId: report.evaluation, budgetUsd: cap }).unwrap();
					} catch {
						// Shown below from the mutation state.
					}
				}}
				testId="cheat-check"
			/>
			{started.isSuccess && <p className="text-muted-foreground">{t("cheat.checkStarted")}</p>}
			{started.error && <p className="text-destructive">{getBenchErrorMessage(started.error)}</p>}
		</div>
	);
}
