import { faultName } from "@/components/reportcard/reportCard.utils";
import { Button } from "@/components/ui/button";
import { useStartDebugRunMutation } from "@/lib/store/apis/runDebugApi";
import type { RunDetail } from "@/lib/types/bench";
import type { DebugRunResponse } from "@/lib/types/runDebug";
import { Bug, Rewind } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { BreakpointDialog, StartedNote, errorText } from "./debugDialogs";

/**
 * The debug actions a run allows (N133). The server computes them with the rule its debug routes apply
 * (`debug_allowed`): a clean run allows everything, a run under a model fault allows Replay only (the
 * recording holds the faulted answers), any other fault allows nothing. An older server sends nothing:
 * then only a clean run allows them.
 */
export function debugAllowed(d: RunDetail): { replay: boolean; fork: boolean } {
	if (d.debug_allowed) return { replay: d.debug_allowed.replay, fork: d.debug_allowed.fork };
	const clean = d.slot.condition === "clean";
	return { replay: clean, fork: clean };
}

/** Whether a run can be replayed: sealed, allowed by its fault, and not itself a replay. */
export function canReplay(d: RunDetail): boolean {
	return Boolean(d.sealed) && debugAllowed(d).replay && d.run.debug?.mode !== "replay";
}

/** Whether a run can be forked from a step or rerun with breakpoints: sealed, without a fault, not itself a replay. */
export function canFork(d: RunDetail): boolean {
	return Boolean(d.sealed) && debugAllowed(d).fork && d.run.debug?.mode !== "replay";
}

/** The readable name of a run's fault condition ("heimdall-model-brownout" reads "Model partly down"). */
export function conditionName(condition: string): string {
	return faultName(condition.replace(/^heimdall-/, ""));
}

/** Why a run under a fault offers fewer debug actions; null when it offers them all. */
export function DebugFaultReason({ detail, testId = "run-debug-reason" }: { detail: RunDetail; testId?: string }) {
	const { t } = useTranslation("runDebug");
	const allowed = debugAllowed(detail);
	if (allowed.replay && allowed.fork) return null;
	const fault = conditionName(detail.slot.condition);
	return (
		<p className="text-muted-foreground max-w-sm text-right text-xs" data-testid={testId}>
			{allowed.replay ? t("actions.forkNeedsClean", { fault }) : t("actions.replayNeedsClean", { fault })}
		</p>
	);
}

/** Run page header actions: Replay (no model cost) and Debug (rerun with breakpoints). */
export function RunDebugActions({ detail }: { detail: RunDetail }) {
	const { t } = useTranslation("runDebug");
	const [start, state] = useStartDebugRunMutation();
	const [started, setStarted] = useState<DebugRunResponse | null>(null);
	const [debugOpen, setDebugOpen] = useState(false);
	const allowed = debugAllowed(detail);
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
				{allowed.fork && (
					<Button variant="outline" size="sm" title={t("actions.debugHint")} onClick={() => setDebugOpen(true)} dataTestId="run-debug">
						<Bug aria-hidden="true" />
						{t("actions.debug")}
					</Button>
				)}
			</div>
			<DebugFaultReason detail={detail} />
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