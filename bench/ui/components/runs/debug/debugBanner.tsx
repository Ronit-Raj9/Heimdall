import { EntityLink } from "@/components/shell/entityLink";
import { formatDuration } from "@/components/runs/traceView.utils";
import { Button } from "@/components/ui/button";
import { useGetRunCallsQuery } from "@/lib/store/apis/runDebugApi";
import { useGetRunStepsQuery } from "@/lib/store/apis/runStepsApi";
import type { StepRecord } from "@/lib/types/bench";
import type { RunCall, RunDebug } from "@/lib/types/runDebug";
import { Link } from "@tanstack/react-router";
import { Bug } from "lucide-react";
import { useContext } from "react";
import { useTranslation } from "react-i18next";
import { ReactReduxContext } from "react-redux";
import { conditionName } from "./debugActions";
import { hw11 } from "./hw11I18n";

/** The run id of the run page the banner is on (/runs/{id}), or "" anywhere else. */
export function pageRunId(): string {
	if (typeof window === "undefined") return "";
	const m = /\/runs\/([^/?#]+)/.exec(window.location.pathname);
	return m ? decodeURIComponent(m[1]) : "";
}

/**
 * The banner sentence of a fork (fix wave 3, N247): what the operator changed at the fork call. A
 * run sealed before run.json carried the edit has none; when one of its calls was edited the plain
 * "Fork of run ... from call ..." sentence stays, since what changed is not known.
 */
export function forkBannerText(debug: RunDebug, calls?: readonly RunCall[]): string | null {
	if (debug.mode !== "fork") return null;
	const vars = { run: debug.from_run ?? "", call: debug.call ?? 0 };
	if (debug.edit === "message") return hw11("banner.forkMessage", vars);
	if (debug.edit === "response") return hw11("banner.forkResponse", vars);
	if (!calls) return null;
	return calls.some((c) => c.replay === "edited") ? null : hw11("banner.forkNone", vars);
}

/** The step of the fork call: the trace row whose gateway request is model call debug.call. */
export function forkStepSeq(call: number | undefined, calls?: readonly RunCall[], steps?: readonly StepRecord[]): number | undefined {
	if (!call || !calls || !steps) return undefined;
	const requestId = calls.find((c) => c.call === call)?.request_id;
	if (!requestId) return undefined;
	const rows = steps.filter((s) => s.request_id === requestId);
	return (rows.find((s) => s.kind === "model_turn") ?? rows[0])?.seq;
}

/** The note at the top of a debug run: what it came from, that it never counts, and time held. */
export function DebugBanner({ debug }: { debug: RunDebug }) {
	// The calls and steps come from the store when there is one (the run page); a banner rendered on
	// its own reads the sentence from run.json only.
	const redux = useContext(ReactReduxContext);
	const runId = pageRunId();
	if (redux && runId && debug.mode === "fork") return <ForkBanner debug={debug} runId={runId} />;
	return <DebugBannerView debug={debug} />;
}

/** A fork's banner with its calls and steps, so it can say what changed and select that step. */
function ForkBanner({ debug, runId }: { debug: RunDebug; runId: string }) {
	const calls = useGetRunCallsQuery(runId);
	const steps = useGetRunStepsQuery(runId);
	return <DebugBannerView debug={debug} runId={runId} calls={calls.data?.calls} steps={steps.data?.steps} />;
}

/** The banner itself, data in props. */
export function DebugBannerView({
	debug,
	runId,
	calls,
	steps,
}: {
	debug: RunDebug;
	runId?: string;
	calls?: readonly RunCall[];
	steps?: readonly StepRecord[];
}) {
	const { t } = useTranslation("runDebug");
	const run = debug.from_run ?? "";
	const fork = forkBannerText(debug, calls);
	const seq = runId ? forkStepSeq(debug.call, calls, steps) : undefined;
	return (
		<div className="bg-muted/40 flex flex-col gap-1 rounded-xl border px-4 py-3 text-sm" data-testid="debug-banner" data-mode={debug.mode}>
			<span className="flex flex-wrap items-center gap-2 font-medium">
				<Bug className="text-muted-foreground size-4" aria-hidden="true" />
				<span data-testid="debug-banner-text">{fork ?? t(`banner.${debug.mode}`, { run, call: debug.call ?? 0 })}</span>
				{run && <EntityLink kind="run" id={run} />}
				{runId && seq !== undefined && (
					<Button variant="outline" size="sm" asChild>
						<Link
							to="/runs/$runId"
							params={{ runId }}
							search={{ tab: "timeline", step: seq } as never}
							data-testid="debug-banner-show-step"
						>
							{hw11("banner.showStep")}
						</Link>
					</Button>
				)}
			</span>
			{debug.from_condition && (
				<span className="text-muted-foreground text-xs" data-testid="debug-banner-condition">
					{t("banner.fromCondition", { fault: conditionName(debug.from_condition) })}
				</span>
			)}
			<span className="text-muted-foreground text-xs">{t("banner.note")}</span>
			{debug.held_ms ? (
				<span className="text-muted-foreground text-xs">{t("banner.held", { time: formatDuration(debug.held_ms) })}</span>
			) : null}
		</div>
	);
}