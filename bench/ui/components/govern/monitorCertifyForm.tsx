import { isCheckPracticeOrDebug } from "@/components/experiments/experiments.utils";
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { useListProjectFilesQuery } from "@/lib/store/apis/projectFilesApi";
import type { ExperimentStatus } from "@/lib/types/bench";
import type { ProjectFileList } from "@/lib/types/projectfiles";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Splits a comma or space separated id list, dropping blanks and duplicates. */
export function splitIds(s: string): string[] {
	return [...new Set(s.split(/[\s,]+/).filter(Boolean))];
}

/** Where monitor settings files live: policies/monitors/ (a folder Settings > Files can write). */
export const MONITOR_DIR = "monitors/";

/**
 * The monitor settings files of the project, as paths the job takes (policies/monitors/x.yaml),
 * from a recursive listing of the policies area (fix wave 3, N255). Pure.
 */
export function monitorFiles(list: ProjectFileList | undefined): string[] {
	return (list?.entries ?? [])
		.filter((e) => !e.dir && e.path.startsWith(MONITOR_DIR) && /\.ya?ml$/i.test(e.path))
		.map((e) => `policies/${e.path}`)
		.sort();
}

/** At most this many comparisons are shown at once; a search narrows the rest. */
export const MAX_SHOWN = 50;

/** The server replays at most this many comparisons in one test. */
export const MAX_PICKED = 20;

/**
 * Finished comparisons a monitor can be tested on, newest first: Heimdall's own checks, practice
 * runs and debug runs are left out; query matches the name or id (fix wave 3, N255). Pure.
 */
export function certifyCandidates(experiments: ExperimentStatus[] | undefined, query: string): ExperimentStatus[] {
	const q = query.trim().toLowerCase();
	return (experiments ?? [])
		.filter((e) => e.state === "done" && !isCheckPracticeOrDebug(e))
		.filter((e) => !q || e.name.toLowerCase().includes(q) || e.exp_id.toLowerCase().includes(q))
		.sort((a, b) => (b.finished_at ?? b.frozen_at ?? "").localeCompare(a.finished_at ?? a.frozen_at ?? ""));
}

/** Starts a monitor.certify job (on the worker) from a monitor settings file and finished comparisons. */
export function MonitorCertifyForm({ onDone }: { onDone?(): void }) {
	const { t } = useTranslation("govern");
	const files = useListProjectFilesQuery({ area: "policies", recursive: true });
	const experiments = useListExperimentsQuery();
	const [monitor, setMonitor] = useState("");
	const [picked, setPicked] = useState<string[]>([]);
	const [search, setSearch] = useState("");
	const [fpr, setFpr] = useState("0.01,0.001");
	const [gradeSource, setGradeSource] = useState("original");
	const monitors = monitorFiles(files.data);
	const candidates = certifyCandidates(experiments.data?.experiments, search);
	const shown = candidates.slice(0, MAX_SHOWN);
	const ready = monitor !== "" && picked.length > 0 && picked.length <= MAX_PICKED;
	const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
	return (
		<Card data-testid="monitor-certify">
			<CardContent className="flex flex-col gap-3">
				<h2 className="text-sm font-medium">{t("monitors.certifyTitle")}</h2>
				<p className="text-muted-foreground text-xs">{t("monitors.certifyHint")}</p>
				<label className="flex flex-col gap-1 text-xs">
					{t("monitors.monitorFile")}
					{monitors.length > 0 ? (
						<select
							className="border-input bg-background h-9 rounded-md border px-2 text-sm"
							value={monitor}
							onChange={(e) => setMonitor(e.target.value)}
							data-testid="monitor-certify-file"
						>
							<option value="">{t("monitors.monitorFilePick")}</option>
							{monitors.map((m) => (
								<option key={m} value={m}>
									{m}
								</option>
							))}
						</select>
					) : (
						<span className="text-muted-foreground" data-testid="monitor-certify-no-file">
							{files.isLoading ? t("monitors.loading") : t("monitors.monitorFilesEmpty")}
						</span>
					)}
				</label>
				<fieldset className="flex flex-col gap-2 text-xs" data-testid="monitor-certify-exps">
					<legend className="mb-1">{t("monitors.comparisons", { max: MAX_PICKED })}</legend>
					<Input
						value={search}
						placeholder={t("monitors.comparisonsSearch")}
						onChange={(e) => setSearch(e.target.value)}
						data-testid="monitor-certify-search"
					/>
					{shown.length === 0 ? (
						<p className="text-muted-foreground" data-testid="monitor-certify-no-exps">
							{experiments.isLoading ? t("monitors.loading") : t("monitors.comparisonsEmpty")}
						</p>
					) : (
						<ul className="flex max-h-56 flex-col gap-1 overflow-y-auto rounded-md border p-2">
							{shown.map((e) => (
								<li key={e.exp_id}>
									<label className="flex items-center gap-2">
										<input
											type="checkbox"
											checked={picked.includes(e.exp_id)}
											onChange={() => toggle(e.exp_id)}
											data-testid={`monitor-certify-exp-${e.exp_id}`}
										/>
										<span className="min-w-0 truncate">{e.name || e.exp_id}</span>
										<span className="text-muted-foreground ml-auto shrink-0">{(e.finished_at ?? e.frozen_at ?? "").slice(0, 10)}</span>
									</label>
								</li>
							))}
						</ul>
					)}
					{candidates.length > shown.length && (
						<p className="text-muted-foreground">{t("monitors.comparisonsMore", { count: candidates.length - shown.length })}</p>
					)}
					{picked.length > MAX_PICKED && <p className="text-destructive">{t("monitors.comparisonsTooMany", { max: MAX_PICKED })}</p>}
				</fieldset>
				<div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
					<label className="flex flex-col gap-1 text-xs">
						{t("monitors.fpr")}
						<Input value={fpr} onChange={(e) => setFpr(e.target.value)} />
					</label>
					<label className="flex flex-col gap-1 text-xs">
						{t("monitors.gradeSource")}
						<Input value={gradeSource} onChange={(e) => setGradeSource(e.target.value)} />
					</label>
				</div>
				{ready ? (
					<div>
						<JobRunButton
							kind="monitor.certify"
							params={{ monitor, exp_ids: picked, fpr: fpr.trim(), grade_source: gradeSource.trim() }}
							label={t("monitors.certify")}
							onDone={() => onDone?.()}
						/>
					</div>
				) : (
					<p className="text-muted-foreground text-xs" data-testid="monitor-certify-need">
						{t("monitors.needInput")}
					</p>
				)}
			</CardContent>
		</Card>
	);
}