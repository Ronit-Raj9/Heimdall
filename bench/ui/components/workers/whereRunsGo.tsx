import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { type WorkerTargetChoice, useGetWorkerTargetQuery, useSetWorkerTargetMutation } from "@/lib/store/apis/workerTargetApi";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

function errorStatus(error: unknown): number | undefined {
	const s = (error as { status?: unknown } | undefined)?.status;
	return typeof s === "number" ? s : undefined;
}

/** "Modal (a fresh sandbox per run)", "gpu-box (Fly Machines)", "Shared worker"; sandbox is the localized "a fresh sandbox per run". */
export function choiceLabel(c: WorkerTargetChoice, sandbox: string): string {
	return c.kind === "platform" ? `${c.title} (${sandbox})` : c.title;
}

/**
 * Settings, Workers: the one "where runs go" setting. Every run of the organization goes to the
 * shared worker, a fresh sandbox per run on a connected platform, or one of its own workers.
 * Renders nothing off the hosted platform.
 */
export function WhereRunsGo() {
	const { t } = useTranslation("byoworker");
	const query = useGetWorkerTargetQuery();
	const [save, state] = useSetWorkerTargetMutation();
	const [error, setError] = useState("");
	const fieldId = useId();
	const status = query.isError ? errorStatus(query.error) : undefined;
	if (status === 503 || status === 404) return null;
	const data = query.data;
	const choose = async (target: string) => {
		setError("");
		try {
			await save(target).unwrap();
		} catch (e) {
			setError(getBenchErrorMessage(e));
		}
	};
	return (
		<section className="bg-card flex min-w-0 flex-col gap-2 rounded-xl border px-5 py-4" data-testid="where-runs-go">
			<div className="flex min-w-0 flex-wrap items-center gap-3">
				<label htmlFor={fieldId} className="text-foreground text-sm font-semibold">
					{t("target.label")}
				</label>
				{query.isError ? (
					<span className="text-chart-error-ink text-sm" role="alert">
						{t("target.loadError")}: {getBenchErrorMessage(query.error)}
					</span>
				) : (
					<select
						id={fieldId}
						className="border-input bg-background h-8 min-w-64 rounded-md border px-2 text-sm"
						value={data?.target ?? ""}
						disabled={!data || state.isLoading}
						onChange={(e) => void choose(e.target.value)}
						data-testid="where-runs-go-select"
					>
						{(data?.choices ?? []).map((c) => (
							<option key={c.target} value={c.target}>
								{choiceLabel(c, t("target.sandbox"))}
							</option>
						))}
					</select>
				)}
			</div>
			<p className="text-muted-foreground max-w-3xl text-xs text-pretty">{t("target.hint")}</p>
			{error && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid="where-runs-go-error">
					{t("target.error")}: {error}
				</p>
			)}
		</section>
	);
}

/** Evaluate wizard: pick where an evaluation runs. Renders nothing off the hosted platform or with only the shared worker. */
export function EvaluationWorkerPicker({ value, onChange }: { value: string; onChange(worker: string): void }) {
	const { t } = useTranslation("byoworker");
	const query = useGetWorkerTargetQuery();
	const fieldId = useId();
	const data = query.data;
	if (!data || data.choices.length <= 1) return null;
	const current = data.choices.find((c) => c.current);
	return (
		<section className="flex flex-col gap-1.5" data-testid="eval-worker">
			<label htmlFor={fieldId} className="text-sm font-medium">
				{t("target.evalLabel")}
			</label>
			<select
				id={fieldId}
				className="border-input bg-background h-8 w-72 rounded-md border px-2 text-sm"
				value={value}
				onChange={(e) => onChange(e.target.value)}
				data-testid="eval-worker-select"
			>
				<option value="">
					{current ? `${choiceLabel(current, t("target.sandbox"))}, ${t("target.whereRunsGo")}` : t("target.whereRunsGo")}
				</option>
				{data.choices
					.filter((c) => !c.current)
					.map((c) => (
						<option key={c.target} value={c.target} disabled={!c.per_evaluation} title={c.note}>
							{choiceLabel(c, t("target.sandbox"))}
							{c.kind === "worker" && c.status ? `, ${t(`status.${c.status}`, { defaultValue: c.status }).toLowerCase()}` : ""}
							{c.per_evaluation ? "" : ` (${t("target.notForOne")})`}
						</option>
					))}
			</select>
		</section>
	);
}