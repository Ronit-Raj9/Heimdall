import { Button } from "@/components/ui/button";
import { useStartDebugRunMutation } from "@/lib/store/apis/runDebugApi";
import type { RunDetail } from "@/lib/types/bench";
import type { DebugRunResponse } from "@/lib/types/runDebug";
import { Bug, Rewind } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { BreakpointDialog, StartedNote, errorText } from "./debugDialogs";

/** Whether a run can be replayed or forked: sealed, without a fault, and not itself a replay. */
export function canReplay(d: RunDetail): boolean {
	return Boolean(d.sealed) && d.slot.condition === "clean" && d.run.debug?.mode !== "replay";
}

/** Run page header actions: Replay (no model cost) and Debug (rerun with breakpoints). */
export function RunDebugActions({ detail }: { detail: RunDetail }) {
	const { t } = useTranslation("runDebug");
	const [start, state] = useStartDebugRunMutation();
	const [started, setStarted] = useState<DebugRunResponse | null>(null);
	const [debugOpen, setDebugOpen] = useState(false);
	if (detail.slot.condition !== "clean") return null;
	const runId = detail.run.run_id;
	const replay = async () => {
		const res = await start({ runId, mode: "replay", body: {} });
		if ("data" in res && res.data) setStarted(res.data);
	};
	return (
		<div className="flex flex-col items-end gap-1">
			<div className="flex items-center gap-2">
				{canReplay(detail) && (
					<Button
						variant="outline"
						size="sm"
						title={t("actions.replayHint")}
						onClick={() => void replay()}
						isLoading={state.isLoading}
						disabled={state.isLoading || Boolean(started)}
						dataTestId="run-replay"
					>
						<Rewind aria-hidden="true" />
						{t("actions.replay")}
					</Button>
				)}
				<Button variant="outline" size="sm" title={t("actions.debugHint")} onClick={() => setDebugOpen(true)} dataTestId="run-debug">
					<Bug aria-hidden="true" />
					{t("actions.debug")}
				</Button>
			</div>
			{state.error ? (
				<p className="text-chart-error-ink text-xs" role="alert" data-testid="run-replay-error">
					{errorText(state.error) ?? t("actions.failed")}
				</p>
			) : null}
			<StartedNote started={started} />
			{debugOpen && <BreakpointDialog runId={runId} open onClose={() => setDebugOpen(false)} />}
		</div>
	);
}
