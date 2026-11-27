import { type ConfirmFn, type StrictConfirmOptions, useConfirm } from "@/components/platform/confirmDialog";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import {
	type WorkerTargetChoice,
	type WorkerTargetView,
	useGetWorkerTargetQuery,
	useSetWorkerTargetMutation,
} from "@/lib/store/apis/workerTargetApi";
import type { TFunction } from "i18next";
import { useCallback, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import "./hw05I18n";
import "./w4510I18n";

function errorStatus(error: unknown): number | undefined {
	const s = (error as { status?: unknown } | undefined)?.status;
	return typeof s === "number" ? s : undefined;
}

/** "Modal (a fresh sandbox per run)", "gpu-box (Fly Machines)", "Shared worker"; sandbox is the localized "a fresh sandbox per run". */
export function choiceLabel(c: WorkerTargetChoice, sandbox: string): string {
	return c.kind === "platform" ? `${c.title} (${sandbox})` : c.title;
}

/** The name a confirmation uses for a target: the choice's title ("Modal", "Shared worker"), else the raw target. */
function targetTitle(view: WorkerTargetView | undefined, target: string): string {
	return view?.choices.find((c) => c.target === target)?.title ?? target;
}

/**
 * The confirmation before a change of where runs go (N217). The body says whether runs not started
 * yet move too: they do unless the chosen platform (or, leaving a platform, the shared worker) takes
 * one evaluation on its own (`per_evaluation`); a platform adds that each run is billed to its account.
 */
export function targetConfirm(
	view: WorkerTargetView | undefined,
	target: string,
	t: TFunction<"hw05">,
): { message: string; opts: StrictConfirmOptions } {
	const chosen = view?.choices.find((c) => c.target === target);
	const current = view?.choices.find((c) => c.current) ?? view?.choices.find((c) => c.target === view?.target);
	const name = targetTitle(view, target);
	let perEvaluation = true;
	if (chosen?.kind === "platform") perEvaluation = chosen.per_evaluation;
	else if (current?.kind === "platform") perEvaluation = view?.choices.find((c) => c.kind === "shared")?.per_evaluation ?? false;
	const lines = [t(perEvaluation ? "target.confirmBodyPerEvaluation" : "target.confirmBody", { target: name })];
	if (chosen?.kind === "platform") lines.push(t("target.confirmBilled", { platform: chosen.title }));
	return {
		message: lines.join(" "),
		opts: {
			title: t("target.confirmTitle"),
			confirmLabel: t("target.confirmOk", { target: name }),
			cancelLabel: t("target.confirmKeep", { current: current ? current.title : targetTitle(view, view?.target ?? "") }),
		},
	};
}

/** Asks before a change of where runs go; true when the person confirmed. Choosing the current target asks nothing. */
export function confirmTarget(
	confirm: ConfirmFn,
	view: WorkerTargetView | undefined,
	target: string,
	t: TFunction<"hw05">,
): Promise<boolean> {
	if (view && view.target === target) return Promise.resolve(true);
	const { message, opts } = targetConfirm(view, target, t);
	return confirm(message, opts);
}

/** Saves target only after ask confirmed it; true when it was saved. */
export async function changeTarget(
	ask: (target: string) => Promise<boolean>,
	save: (target: string) => Promise<unknown>,
	target: string,
): Promise<boolean> {
	if (!(await ask(target))) return false;
	await save(target);
	return true;
}

/** The one confirmation every "where runs go" control uses: the Runs go to select, a platform card and an own worker card. */
export function useConfirmTarget(): (target: string) => Promise<boolean> {
	const { t } = useTranslation("hw05");
	const confirm = useConfirm();
	const { data } = useGetWorkerTargetQuery();
	return useCallback((target: string) => confirmTarget(confirm, data, target, t), [confirm, data, t]);
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
	const ask = useConfirmTarget();
	const status = query.isError ? errorStatus(query.error) : undefined;
	if (status === 503 || status === 404) return null;
	const data = query.data;
	const choose = async (target: string) => {
		setError("");
		try {
			// N217: nothing changes until the person confirms; on Cancel the controlled select shows the current target again.
			await changeTarget(ask, (x) => save(x).unwrap(), target);
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

/** Settings, Workers tab: where an admin adds a worker. */
export const ADD_WORKER_HREF = "/settings?tab=runners";

/**
 * Evaluate wizard: pick where an evaluation runs. Renders nothing off the hosted platform. With one
 * choice it says where runs go, how many it runs at a time, and how to add a worker (N93).
 */
export function EvaluationWorkerPicker({ value, onChange }: { value: string; onChange(worker: string): void }) {
	const { t } = useTranslation("byoworker");
	const { t: t4510 } = useTranslation("w4510");
	const session = useSession();
	const query = useGetWorkerTargetQuery();
	const fieldId = useId();
	const data = query.data;
	if (!data || data.choices.length === 0) return null;
	if (data.choices.length === 1) {
		const only = data.choices[0];
		const label = choiceLabel(only, t("target.sandbox"));
		const line = only.slots && only.slots > 0 ? t("target.onlyOneSlots", { label, n: only.slots }) : t("target.onlyOne", { label });
		return (
			<section className="flex flex-col gap-1 text-sm" data-testid="eval-worker-only">
				<p className="text-foreground">{line}</p>
				{canUse(session, "admin") ? (
					<a
						href={ADD_WORKER_HREF}
						className="text-primary w-fit text-xs font-medium underline-offset-4 hover:underline"
						data-testid="eval-worker-add"
					>
						{t("target.addWorker")}
					</a>
				) : (
					<p className="text-muted-foreground text-xs">{t("target.askAdmin")}</p>
				)}
			</section>
		);
	}
	const current = data.choices.find((c) => c.current);
	// N208: a platform picked for this evaluation (or where runs go) takes the evaluation only.
	const picked = value ? data.choices.find((c) => c.target === value) : current;
	const onPlatform = picked?.kind === "platform" && picked.per_evaluation ? picked : undefined;
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
			{onPlatform ? (
				<p className="text-muted-foreground text-xs" data-testid="eval-worker-platform-line">
					{t4510("perEvaluationLine", { platform: onPlatform.title })}
				</p>
			) : null}
		</section>
	);
}