import "@/app/inbox/views/w443I18n";

import { Button } from "@/components/ui/button";
import { getBenchErrorMessage, useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { useRerunFindingMutation, type Finding } from "@/lib/store/apis/findingsApi";
import { useState } from "react";
import { useTranslation } from "react-i18next";

const fieldClass = "border-border bg-background rounded-md border px-2 py-1.5 text-sm";

/**
 * "Check the fix": picks the experiment and arm the failure came from, says what will run, and
 * starts the rerun of the finding's tasks with the old agent and the fixed agent.
 */
export function CheckFixDialog({ finding, onDone }: { finding: Finding; onDone: () => void }) {
	const { t } = useTranslation("w443");
	const experiments = useListExperimentsQuery().data?.experiments ?? [];
	const [expId, setExpId] = useState("");
	const [arm, setArm] = useState("");
	const [trials, setTrials] = useState(3);
	const [rerun, state] = useRerunFindingMutation();
	const [note, setNote] = useState<string | null>(null);
	const arms = experiments.find((e) => e.exp_id === expId)?.arms ?? [];
	const n = finding.tasks.length;

	const start = async () => {
		const res = await rerun({ id: finding.id, body: { exp_id: expId, arm, trials, yes: true } });
		if ("data" in res && res.data) {
			setNote(res.data.note ?? t("checkFix.started"));
		}
	};

	return (
		<div className="border-border flex flex-col gap-3 rounded-lg border p-3" data-testid="check-fix-dialog">
			<label className="flex flex-col gap-1 text-sm">
				{t("checkFix.experiment")}
				<select
					className={fieldClass}
					value={expId}
					onChange={(e) => {
						setExpId(e.target.value);
						setArm("");
					}}
					data-testid="check-fix-experiment"
				>
					<option value="" />
					{experiments.map((e) => (
						<option key={e.exp_id} value={e.exp_id}>
							{e.name}
						</option>
					))}
				</select>
			</label>
			<label className="flex flex-col gap-1 text-sm">
				{t("checkFix.arm")}
				<select className={fieldClass} value={arm} onChange={(e) => setArm(e.target.value)} data-testid="check-fix-arm">
					<option value="" />
					{arms.map((a) => (
						<option key={a} value={a}>
							{a}
						</option>
					))}
				</select>
			</label>
			<label className="flex flex-col gap-1 text-sm">
				{t("checkFix.trials")}
				<input
					type="number"
					min={1}
					max={5}
					className={fieldClass}
					value={trials}
					onChange={(e) => setTrials(Math.min(5, Math.max(1, Number(e.target.value) || 1)))}
					data-testid="check-fix-trials"
				/>
			</label>
			<p className="text-muted-foreground text-sm" data-testid="check-fix-confirm">
				{t("checkFix.confirm", { n, trials })}
			</p>
			{state.error ? (
				<p className="text-destructive text-sm" role="alert" data-testid="check-fix-error">
					{getBenchErrorMessage(state.error)}
				</p>
			) : null}
			{note ? (
				<p className="text-sm" data-testid="check-fix-note">
					{note}
				</p>
			) : null}
			<div className="flex gap-2">
				<Button size="sm" disabled={!expId || !arm || state.isLoading} onClick={() => void start()} data-testid="check-fix-start">
					{t("checkFix.start")}
				</Button>
				<Button size="sm" variant="outline" onClick={onDone}>
					{t("actions.cancel")}
				</Button>
			</div>
		</div>
	);
}