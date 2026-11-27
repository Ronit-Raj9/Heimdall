import "@/app/inbox/views/w443I18n";

import { Button } from "@/components/ui/button";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { getBenchErrorMessage, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { CAP_MAX, CAP_MIN, INVESTIGATION_START_CAP_USD, clampCap, defaultInvestigation, investigableExperiments } from "./findingPlans";

const fieldClass = "border-border bg-background rounded-md border px-2 py-1.5 text-sm";

/**
 * "Find out why": picks a comparison (the newest with failed runs first) and a spending cap held
 * between 0.1 and 5 dollars, says what will run and what it may cost, and starts the
 * findings.investigate job (POST /jobs) only after the shared paid confirm. When the server does not know the job
 * yet, its own message is shown.
 */
export function InvestigateDialog({ onDone }: { onDone: () => void }) {
	const { t } = useTranslation("w443");
	const raw = useListExperimentsQuery().data?.experiments;
	const experiments = useMemo(() => investigableExperiments(raw ?? []), [raw]);
	const [expId, setExpId] = useState("");
	const [touched, setTouched] = useState(false);
	const [capText, setCapText] = useState(String(INVESTIGATION_START_CAP_USD));
	const [create, state] = useCreateJobMutation();
	const [started, setStarted] = useState(false);
	const cap = clampCap(capText);

	useEffect(() => {
		if (!touched && !expId) {
			const first = defaultInvestigation(experiments);
			if (first) setExpId(first);
		}
	}, [experiments, expId, touched]);

	const name = experiments.find((e) => e.exp_id === expId)?.name ?? expId;
	// Paid work: PaidButton always asks first, naming what runs and the cap (M7-26).
	const start = async () => {
		const res = await create({
			kind: "findings.investigate",
			params: { exp_id: expId, budget_usd: cap, yes: true },
		});
		if ("data" in res && res.data) {
			setStarted(true);
			notify({ text: t("started", { ns: "toast", name: t("investigations.verb") }) });
		}
	};

	return (
		<div className="border-border flex flex-col gap-3 rounded-lg border p-3" data-testid="investigate-dialog">
			<label className="flex flex-col gap-1 text-sm">
				{t("investigations.experiment")}
				<select
					className={fieldClass}
					value={expId}
					onChange={(e) => {
						setTouched(true);
						setExpId(e.target.value);
					}}
					data-testid="investigate-experiment"
				>
					<option value="" />
					{experiments.map((e) => (
						<option key={e.exp_id} value={e.exp_id}>
							{t("investigations.option", {
								name: e.name,
								count: e.slots?.failed ?? 0,
							})}
						</option>
					))}
				</select>
			</label>
			<label className="flex flex-col gap-1 text-sm">
				{t("investigations.cap")}
				<input
					type="number"
					min={CAP_MIN}
					max={CAP_MAX}
					step={0.1}
					className={fieldClass}
					value={capText}
					onChange={(e) => setCapText(e.target.value)}
					onBlur={() => setCapText(String(cap))}
					data-testid="investigate-cap"
				/>
			</label>
			{state.error ? (
				<p className="text-destructive text-sm" role="alert" data-testid="investigate-error">
					{getBenchErrorMessage(state.error)}
				</p>
			) : null}
			{started ? <p className="text-sm">{t("investigations.started")}</p> : null}
			<div className="flex flex-wrap gap-2">
				<PaidButton
					size="sm"
					verb={t("investigations.verb")}
					capUsd={cap}
					confirm="always"
					kind="form"
					confirmText={t("investigations.ask", { name, cap: cap.toFixed(2) })}
					disabled={!expId || state.isLoading || started}
					onConfirm={start}
					testId="investigate-start"
				/>
				<Button size="sm" variant="outline" onClick={onDone}>
					{t("actions.cancel")}
				</Button>
			</div>
		</div>
	);
}