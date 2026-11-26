import "@/app/inbox/views/w443I18n";

import { Button } from "@/components/ui/button";
import { getBenchErrorMessage, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

const fieldClass = "border-border bg-background rounded-md border px-2 py-1.5 text-sm";

/**
 * "Find out why": picks an experiment and a spending cap and starts the findings.investigate job
 * (POST /jobs). When the server does not know the job yet, its own message is shown.
 */
export function InvestigateDialog({ onDone }: { onDone: () => void }) {
	const { t } = useTranslation("w443");
	const experiments = useListExperimentsQuery().data?.experiments ?? [];
	const [expId, setExpId] = useState("");
	const [cap, setCap] = useState(1);
	const [create, state] = useCreateJobMutation();
	const [started, setStarted] = useState(false);

	const start = async () => {
		const res = await create({ kind: "findings.investigate", params: { exp_id: expId, budget_usd: cap, yes: true } });
		if ("data" in res && res.data) setStarted(true);
	};

	return (
		<div className="border-border flex flex-col gap-3 rounded-lg border p-3" data-testid="investigate-dialog">
			<label className="flex flex-col gap-1 text-sm">
				{t("investigations.experiment")}
				<select className={fieldClass} value={expId} onChange={(e) => setExpId(e.target.value)} data-testid="investigate-experiment">
					<option value="" />
					{experiments.map((e) => (
						<option key={e.exp_id} value={e.exp_id}>
							{e.name}
						</option>
					))}
				</select>
			</label>
			<label className="flex flex-col gap-1 text-sm">
				{t("investigations.cap")}
				<input
					type="number"
					min={0.1}
					max={5}
					step={0.1}
					className={fieldClass}
					value={cap}
					onChange={(e) => setCap(Number(e.target.value) || 0)}
					data-testid="investigate-cap"
				/>
			</label>
			{state.error ? (
				<p className="text-destructive text-sm" role="alert" data-testid="investigate-error">
					{getBenchErrorMessage(state.error)}
				</p>
			) : null}
			{started ? <p className="text-sm">{t("investigations.started")}</p> : null}
			<div className="flex gap-2">
				<Button size="sm" disabled={!expId || cap <= 0 || state.isLoading} onClick={() => void start()} data-testid="investigate-start">
					{t("investigations.start")}
				</Button>
				<Button size="sm" variant="outline" onClick={onDone}>
					{t("actions.cancel")}
				</Button>
			</div>
		</div>
	);
}