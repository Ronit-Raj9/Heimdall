import "@/app/inbox/views/w443I18n";

import { GroupedModelPicker, type PickedModel } from "@/components/models/groupedModelPicker";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/select";
import { PaidButton } from "@/components/ui/paidButton";
import { notify } from "@/lib/notify";
import { getBenchErrorMessage, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { useCreateJobMutation } from "@/lib/store/apis/jobRunApi";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	CAP_MAX,
	CAP_MIN,
	INVESTIGATION_START_CAP_USD,
	clampCap,
	defaultInvestigation,
	failedExperiments,
	investigationBody,
} from "./findingPlans";
import { defaultJobModel, plainJobError } from "./jobModel";
import "./uff1I18n";

const fieldClass = "border-border bg-background rounded-md border px-2 py-1.5 text-sm";

/**
 * "Find out why": picks a comparison (the newest with failed runs first) and a spending cap held
 * between 0.1 and 5 dollars, says what will run and what it may cost, and starts the
 * findings.investigate job (POST /jobs) only after the shared paid confirm. When the server does not know the job
 * yet, its own message is shown.
 */
function optionLabel(
	u: (k: "investigate.option" | "investigate.optionNoDate", o: Record<string, unknown>) => string,
	name: string,
	count: number,
	at?: string,
): string {
	const d = at ? new Date(at) : null;
	if (!d || Number.isNaN(d.getTime())) return u("investigate.optionNoDate", { name, count });
	return u("investigate.option", { name, count, date: d.toLocaleDateString("en-GB", { day: "numeric", month: "short" }) });
}

export function InvestigateDialog({ onDone }: { onDone: () => void }) {
	const { t } = useTranslation("w443");
	const { t: u } = useTranslation("uff1");
	const raw = useListExperimentsQuery().data?.experiments;
	// Only comparisons with failed runs can be investigated (PH2-5); the rest are counted, not listed.
	const experiments = useMemo(() => failedExperiments(raw ?? []), [raw]);
	const hidden = (raw?.length ?? 0) - experiments.length;
	const models = useGetModelsQuery();
	const [picked, setPicked] = useState<PickedModel | null>(null);
	const fallback = useMemo(() => defaultJobModel(models.data), [models.data]);
	// The server refuses a job with no model (PH2-1): the form opens on the org's default working model.
	const model = picked ?? fallback;
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
	const modelName = model?.model ?? "";
	// Paid work: PaidButton always asks first, naming what runs and the cap (M7-26).
	const start = async () => {
		const res = await create({
			kind: "findings.investigate",
			params: investigationBody(expId, model, cap),
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
				<NativeSelect
					wrapperClassName="w-full"
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
							{optionLabel(u, e.name, e.slots?.failed ?? 0, e.finished_at ?? e.started_at)}
						</option>
					))}
				</NativeSelect>
			</label>
			{experiments.length === 0 ? (
				<p className="text-muted-foreground text-sm" data-testid="investigate-none">
					{u("investigate.none")}
				</p>
			) : hidden > 0 ? (
				<p className="text-muted-foreground text-xs" data-testid="investigate-hidden">
					{u("investigate.hidden", { count: hidden })}
				</p>
			) : null}
			<div className="flex flex-col gap-1 text-sm">
				<span>{u("model.label")}</span>
				<GroupedModelPicker value={model} onChange={setPicked} label={u("model.label")} testId="investigate-model" />
				{models.data && !model ? (
					<p className="text-muted-foreground text-xs" data-testid="investigate-no-model">
						{u("model.noKey")}
					</p>
				) : null}
			</div>
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
					{plainJobError(getBenchErrorMessage(state.error))}
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
					confirmText={u("investigate.ask", { name, model: modelName, cap: cap.toFixed(2) })}
					disabled={!expId || !modelName || state.isLoading || started}
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