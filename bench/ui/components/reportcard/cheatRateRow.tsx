// Cheat rate on impossible twins (W4-48, design D4 10.5): how often each arm passed a task that cannot honestly be
// passed. Shown on the report card when the server sends the cheat check row; a rate is always shown with its counts.
import "@/components/issuebench/w448I18n";
import { Button } from "@/components/ui/button";
import { Section } from "@/components/ui/section";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useStartCheatCheckMutation } from "@/lib/store/apis/cheatCheckApi";
import type { Report } from "@/lib/store/apis/evaluationsApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { Link } from "@tanstack/react-router";
import { useContext, useState } from "react";
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
		<Section title={t("cheat.title")} description={t("cheat.description")}>
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
				{cc.state === "no_twins" && <p>{tr("cheat.notRun")}</p>}
				{store && cc.state === "not_measured" && <CheatActions report={report} cc={cc} />}
			</div>
		</Section>
	);
}

function CheatActions({ report, cc }: { report: Report; cc: CheatCheck }) {
	const { t } = useTranslation("w448");
	const [start, started] = useStartCheatCheckMutation();
	const [makeJob, made] = useCreateJobMutation();
	const [confirm, setConfirm] = useState(false);
	const suites = [...new Set(report.benchmarks.map((b) => b.benchmark.split("@")[0]))].filter((b) => !b.endsWith("-twins"));
	const error = started.error ?? made.error;
	if (cc.state === "no_twins") {
		return (
			<div className="flex flex-col gap-1">
				<Button
					size="sm"
					variant="outline"
					className="w-fit"
					disabled={made.isLoading || made.isSuccess || suites.length === 0}
					dataTestId="cheat-make-twins"
					onClick={() => suites.forEach((suite) => void makeJob({ kind: "twins.make", params: { suite, max: 50 } }))}
				>
					{t("cheat.makeTwins")}
				</Button>
				{made.isSuccess && <p className="text-muted-foreground">{t("cheat.twinsStarted")}</p>}
				{error && <p className="text-destructive">{getBenchErrorMessage(error)}</p>}
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-1">
			{!confirm ? (
				<Button size="sm" variant="outline" className="w-fit" dataTestId="cheat-check" onClick={() => setConfirm(true)}>
					{t("cheat.check")}
				</Button>
			) : (
				<>
					<p>{t("cheat.confirm")}</p>
					<Button
						size="sm"
						className="w-fit"
						disabled={started.isLoading || started.isSuccess}
						dataTestId="cheat-check-start"
						onClick={() => void start(report.evaluation)}
					>
						{t("cheat.start")}
					</Button>
				</>
			)}
			{started.isSuccess && <p className="text-muted-foreground">{t("cheat.checkStarted")}</p>}
			{error && <p className="text-destructive">{getBenchErrorMessage(error)}</p>}
		</div>
	);
}