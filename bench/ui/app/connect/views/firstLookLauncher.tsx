import { Button } from "@/components/ui/button";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { usePlanFirstLookMutation } from "@/lib/store/apis/connectApi";
import { useCreateEvaluationMutation } from "@/lib/store/apis/evaluationsApi";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { estimateMinutes } from "./connect.utils";

/** The line every first-look evaluation shows: too small for a verdict, never a gate. */
export function FirstLookNote() {
	const { t } = useTranslation("connect");
	return (
		<p className="border-chart-info/30 bg-chart-info/5 rounded-lg border px-3 py-2 text-sm" data-testid="first-look-note">
			{t("firstLook.note")} {t("firstLook.notGate")}
		</p>
	);
}

/**
 * First look: Heimdall picks the fastest compatible benchmark for the agent (POST
 * /connect/first-look), shows why, the size and the reference row when there is one, then starts
 * the evaluation with the first_look preset and opens it.
 */
export function FirstLookLauncher({ agent }: { agent: string }) {
	const { t } = useTranslation("connect");
	const navigate = useNavigate();
	const [plan, planState] = usePlanFirstLookMutation();
	const [create, createState] = useCreateEvaluationMutation();
	const data = planState.data;
	const start = async () => {
		if (!data) return;
		const res = await create(data.evaluation);
		if ("data" in res && res.data) void navigate({ to: "/evaluations/$evalId", params: { evalId: res.data.id } });
	};
	const minutes = data ? estimateMinutes(data.pick.estimate_s) : 0;
	const ref = data?.reference;
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
						{minutes > 0 ? t("firstLook.estimate", { minutes }) : t("firstLook.estimateUnknown")}
					</p>
					{ref && typeof ref.score === "number" && (
						<p className="text-sm" data-testid="first-look-reference">
							{t("firstLook.reference", { agent: ref.agent ?? "", model: ref.model ?? "", score: Math.round(ref.score * 100) })}
						</p>
					)}
					<FirstLookNote />
					<div>
						<Button onClick={() => void start()} isLoading={createState.isLoading} data-testid="first-look-start">
							{createState.isLoading ? t("firstLook.starting") : t("firstLook.start")}
						</Button>
					</div>
					{createState.error ? <p className="text-chart-error-ink text-sm">{getBenchErrorMessage(createState.error)}</p> : null}
				</>
			)}
		</div>
	);
}