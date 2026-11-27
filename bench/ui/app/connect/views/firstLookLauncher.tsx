import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import "@/components/improvements/uf2bI18n";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { usePlanFirstLookMutation } from "@/lib/store/apis/connectApi";
import { useCreateEvaluationMutation } from "@/lib/store/apis/evaluationsApi";
import type { EvaluationSpec } from "@/lib/store/apis/evaluationsApi";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { estimateMinutes } from "./connect.utils";

/**
 * The estimate_usd a first-look plan may carry (G1: POST /connect/first-look?estimate=1 adds
 * {"low","high","known","reason"} to the response). Older servers send nothing; a bare number on
 * the pick is still read.
 */
export type FirstLookUsd = number | { low?: number; high?: number; known?: boolean; reason?: string } | null | undefined;

/** The upper dollar estimate of a plan, or undefined when the server did not send a known one. */
export function firstLookUsd(raw: FirstLookUsd): number | undefined {
	if (typeof raw === "number") return Number.isFinite(raw) ? raw : undefined;
	if (!raw || raw.known === false) return undefined;
	const v = typeof raw.high === "number" ? raw.high : raw.low;
	return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

/**
 * The time and cost line of a first-look plan: minutes, and dollars when the plan sends an
 * estimate_usd (otherwise only the time and "on your model key").
 */
export type FirstLookEstimate = { kind: "unknown" } | { kind: "time"; minutes: number } | { kind: "cost"; minutes: number; usd: string };

export function firstLookEstimate(minutes: number, usd: FirstLookUsd): FirstLookEstimate {
	if (minutes <= 0) return { kind: "unknown" };
	const v = firstLookUsd(usd);
	if (v !== undefined) return { kind: "cost", minutes, usd: v.toFixed(2) };
	return { kind: "time", minutes };
}

/**
 * The line every first-look evaluation shows: the picked task count, too small for a verdict, never a
 * gate. Nothing is shown when the count is not known.
 */
export function FirstLookNote({ tasks }: { tasks?: number }) {
	const { t } = useTranslation("connect");
	if (tasks === undefined) return null;
	return (
		<p className="border-chart-info/30 bg-chart-info/5 rounded-lg border px-3 py-2 text-sm" data-testid="first-look-note">
			{t("firstLook.note", { count: tasks })} {t("firstLook.notGate")}
		</p>
	);
}

/**
 * The optional second card of a first look: the agent against the bare model (the model alone, no
 * tools) on the same benchmark and size. The first look itself stays as it is.
 */
export function BareModelCard({
	spec,
	reason,
	onStart,
	loading,
	error,
}: {
	spec?: EvaluationSpec;
	reason?: string;
	onStart: () => Promise<unknown> | void;
	loading?: boolean;
	error?: string;
}) {
	const { t } = useTranslation("connect");
	const { t: tu } = useTranslation("uf2b");
	if (!spec) {
		return reason ? (
			<p className="text-muted-foreground text-sm" data-testid="first-look-bare-none">
				{t("firstLook.bareNone", { reason })}
			</p>
		) : null;
	}
	return (
		<div className="flex flex-col gap-2 rounded-lg border p-3" data-testid="first-look-bare">
			<div className="text-sm font-medium">{t("firstLook.bareTitle")}</div>
			<p className="text-muted-foreground text-sm">
				{t("firstLook.bareHelp", {
					tasks: spec.tasks_per_benchmark,
					benchmark: spec.benchmarks?.[0] ?? "",
					model: spec.arms[1]?.model ?? spec.arms[0]?.model ?? "",
				})}
			</p>
			<div>
				<PaidButton
					variant="outline"
					verb={t("firstLook.bareStart")}
					capUsd={null}
					confirm="auto"
					confirmText={tu("connect.bareConfirm")}
					onConfirm={onStart}
					isLoading={loading}
					testId="first-look-bare-start"
				/>
			</div>
			{error ? <p className="text-chart-error-ink text-sm">{error}</p> : null}
		</div>
	);
}

/**
 * First look: Heimdall picks the fastest compatible benchmark for the agent (POST
 * /connect/first-look), shows why, the size and the reference row when there is one, then starts
 * the evaluation with the first_look preset and opens it.
 */
export function FirstLookLauncher({ agent }: { agent: string }) {
	const { t } = useTranslation("connect");
	const { t: tu } = useTranslation("uf2b");
	const navigate = useNavigate();
	const [plan, planState] = usePlanFirstLookMutation();
	const [create, createState] = useCreateEvaluationMutation();
	const [createBare, bareState] = useCreateEvaluationMutation();
	const data = planState.data;
	const start = async () => {
		if (!data) return;
		// Real tasks on the user's model key: PaidButton asks first (the cap is unknown, so auto always asks).
		const res = await create(data.evaluation);
		if ("data" in res && res.data) void navigate({ to: "/evaluations/$evalId", params: { evalId: res.data.id } });
	};
	const startBare = async () => {
		if (!data?.bare_model) return;
		const res = await createBare(data.bare_model);
		if ("data" in res && res.data) void navigate({ to: "/evaluations/$evalId", params: { evalId: res.data.id } });
	};
	const minutes = data ? estimateMinutes(data.pick.estimate_s) : 0;
	const ref = data?.reference;
	// estimate_usd is not in the plan type: read it, as optional, from the response or the pick.
	const rawUsd =
		(data as { estimate_usd?: FirstLookUsd } | undefined)?.estimate_usd ??
		(data?.pick as { estimate_usd?: FirstLookUsd } | undefined)?.estimate_usd;
	const est = firstLookEstimate(minutes, rawUsd);
	return (
		<div className="flex flex-col gap-3" data-testid="first-look">
			{!data && (
				<div>
					<Button onClick={() => void plan({ agent })} isLoading={planState.isLoading} data-testid="first-look-plan">
						{planState.isLoading ? t("firstLook.planning") : t("firstLook.plan")}
					</Button>
				</div>
			)}
			{planState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(planState.error)}</p> : null}
			{data && (
				<>
					<div className="text-sm font-medium" data-testid="first-look-pick">
						{t("firstLook.pick", {
							benchmark: data.pick.benchmark,
							tasks: data.pick.tasks,
							trials: data.pick.trials,
							concurrency: data.pick.concurrency,
						})}
					</div>
					<p className="text-muted-foreground text-sm">{data.pick.reason}</p>
					<p className="text-muted-foreground text-sm">
						{est.kind === "cost"
								? tu("connect.firstLookUsd", { minutes: est.minutes, usd: est.usd })
								: est.kind === "time"
									? t("firstLook.estimate", { minutes: est.minutes })
									: t("firstLook.estimateUnknown")}
					</p>
					{ref && typeof ref.score === "number" && (
						<p className="text-sm" data-testid="first-look-reference">
							{t("firstLook.reference", { agent: ref.agent ?? "", model: ref.model ?? "", score: Math.round(ref.score * 100) })}
						</p>
					)}
					<FirstLookNote tasks={data.pick.tasks} />
					<div>
						<PaidButton
							verb={tu("connect.firstLookVerb")}
							capUsd={null}
							estimateUsd={firstLookUsd(rawUsd) ?? null}
							confirm="auto"
							confirmText={tu("connect.firstLookConfirm")}
							onConfirm={start}
							isLoading={createState.isLoading}
							testId="first-look-start"
						/>
					</div>
					{createState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(createState.error)}</p> : null}
					<BareModelCard
						spec={data.bare_model}
						reason={data.bare_model_reason}
						onStart={startBare}
						loading={bareState.isLoading}
						error={bareState.error ? getBenchErrorMessage(bareState.error) : undefined}
					/>
				</>
			)}
		</div>
	);
}