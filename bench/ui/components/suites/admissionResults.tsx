import type { JsonValue } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { CheckCircle2, ChevronRight, XCircle } from "lucide-react";
import { useTranslation } from "react-i18next";

/** One check of one task's readiness (admission) record. */
export interface ReadinessCheck {
	name: string;
	want: string;
	got: string;
	passed: boolean;
	detail?: string;
}

/** One task's readiness result. */
export interface ReadinessTask {
	task: string;
	ready: boolean;
	reason?: string;
	checks: ReadinessCheck[];
}

/** A file problem static validation found before anything ran. */
export interface ReadinessProblem {
	path: string;
	severity: string;
	message: string;
}

export interface ReadinessReport {
	tasks: ReadinessTask[];
	problems: ReadinessProblem[];
}

type Obj = Record<string, JsonValue>;
const isObj = (v: unknown): v is Obj => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown) => (typeof v === "string" ? v : "");

function checksOf(v: unknown): ReadinessCheck[] {
	if (!Array.isArray(v)) return [];
	return v.filter(isObj).map((c) => ({
		name: str(c.name),
		want: str(c.want),
		got: str(c.got),
		passed: c.passed === true,
		detail: str(c.detail) || undefined,
	}));
}

/**
 * The per-task results of a suite.admit job, from either result shape: local admission
 * ({tasks: {id: record}, order, problems}) or hosted admission on a worker
 * ({tasks: [{task, oracle, noop, admitted, reason}]}). Null when the result holds neither.
 */
export function readinessReport(result: unknown): ReadinessReport | null {
	if (!isObj(result)) return null;
	const problems: ReadinessProblem[] = Array.isArray(result.problems)
		? result.problems.filter(isObj).map((p) => ({ path: str(p.path), severity: str(p.severity), message: str(p.message) }))
		: [];
	const tasks = result.tasks;
	if (Array.isArray(tasks)) {
		return {
			problems,
			tasks: tasks.filter(isObj).map((t) => {
				const right = str(t.oracle);
				const blank = str(t.noop);
				return {
					task: str(t.task),
					ready: t.admitted === true,
					reason: str(t.reason) || undefined,
					checks: [
						{ name: "reference_0", want: "pass", got: right, passed: right === "pass" },
						{ name: "empty_control", want: "fail", got: blank, passed: blank !== "" && blank !== "pass" },
					],
				};
			}),
		};
	}
	if (isObj(tasks)) {
		const order = Array.isArray(result.order) ? result.order.map(str).filter(Boolean) : Object.keys(tasks);
		return {
			problems,
			tasks: order
				.filter((id) => isObj(tasks[id]))
				.map((id) => {
					const rec = tasks[id] as Obj;
					return { task: id, ready: rec.admitted === true, checks: checksOf(rec.checks) };
				}),
		};
	}
	return problems.length ? { tasks: [], problems } : null;
}

/** A check's name in plain words: "Wrong answer 2 is caught", "The right answer passes". */
export function checkLabel(name: string, t: (key: string, opts?: Record<string, unknown>) => string): string {
	const n = name.match(/^(reference|mutant|wrong_answer|honest_original)_(\d+)$/);
	if (n) {
		const i = Number(n[2]);
		if (n[1] === "reference") return t("readiness.check.reference", { n: i + 1 });
		if (n[1] === "honest_original") return t("readiness.check.honest", { n: i + 1 });
		return t(n[1] === "mutant" ? "readiness.check.mutant" : "readiness.check.wrongAnswer", { n: i });
	}
	const key = {
		static_validation: "static",
		empty_control: "empty",
		empty_patch: "empty",
		exploit: "exploit",
		instruction_edited: "instruction",
		secret_scan: "secrets",
		verifier_no_network: "network",
		reference_stale_notes: "staleNotes",
		null_noop: "doNothing",
		null_refuse: "refuse",
		oracle_control: "reference1",
		noop_control: "empty",
	}[name];
	return key ? t(`readiness.check.${key}`, { n: 1 }) : name.replace(/_/g, " ");
}

/** Per-task, per-check results of a readiness run. */
export function ReadinessResults({ report }: { report: ReadinessReport }) {
	const { t } = useTranslation("suiteBuild");
	const ready = report.tasks.filter((x) => x.ready).length;
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="readiness-results">
			{report.tasks.length > 0 && (
				<p className="text-sm" data-testid="readiness-summary">
					{t("readiness.summary", { ready, total: report.tasks.length })}
				</p>
			)}
			{report.problems.length > 0 && (
				<ul className="flex flex-col gap-1 rounded-md border px-3 py-2 text-xs" data-testid="readiness-problems">
					{report.problems.map((p, i) => (
						<li key={`${i}-${p.path}`} className={p.severity === "error" ? "text-chart-error-ink" : "text-muted-foreground"}>
							<span className="font-mono">{p.path}</span>: {p.message}
						</li>
					))}
				</ul>
			)}
			<ul className="divide-border flex flex-col divide-y rounded-md border">
				{report.tasks.map((task) => (
					<li key={task.task} data-testid={`readiness-task-${task.task}`}>
						<details open={!task.ready} className="group">
							<summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm">
								<ChevronRight className="text-muted-foreground size-3.5 transition-transform group-open:rotate-90" aria-hidden="true" />
								{task.ready ? (
									<CheckCircle2 className="text-chart-success-ink size-4" aria-hidden="true" />
								) : (
									<XCircle className="text-chart-error-ink size-4" aria-hidden="true" />
								)}
								<span className="font-mono">{task.task}</span>
								<span className={cn("ml-auto text-xs", task.ready ? "text-chart-success-ink" : "text-chart-error-ink")}>
									{task.ready ? t("readiness.ready") : t("readiness.notReady")}
								</span>
							</summary>
							<div className="flex flex-col gap-1 px-9 pb-3">
								{task.reason && <p className="text-chart-error-ink text-xs">{task.reason}</p>}
								<table className="w-full text-xs">
									<tbody>
										{task.checks.map((c, i) => (
											<tr key={`${c.name}-${i}`} data-testid={`readiness-check-${task.task}-${c.name}`}>
												<td className="w-5 py-0.5 align-top">
													{c.passed ? (
														<CheckCircle2 className="text-chart-success-ink size-3.5" aria-label={t("readiness.passed")} />
													) : (
														<XCircle className="text-chart-error-ink size-3.5" aria-label={t("readiness.failed")} />
													)}
												</td>
												<td className="py-0.5 pr-3 align-top">{checkLabel(c.name, t)}</td>
												<td className="text-muted-foreground py-0.5 align-top">
													{c.got ? t("readiness.got", { got: c.got, want: c.want }) : null}
													{c.detail ? <span className="block break-words">{c.detail}</span> : null}
												</td>
											</tr>
										))}
									</tbody>
								</table>
							</div>
						</details>
					</li>
				))}
			</ul>
		</div>
	);
}
