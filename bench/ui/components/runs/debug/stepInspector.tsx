import { JsonBlock } from "@/components/runs/traceView";
import { stepIO } from "@/components/runs/traceView.utils";
import { clipText } from "@/components/runs/stepTimeline.utils";
import { Button } from "@/components/ui/button";
import { useGetRunCallQuery } from "@/lib/store/apis/runDebugApi";
import type { StepRecord } from "@/lib/types/bench";
import type { DebugRunResponse, RunCall } from "@/lib/types/runDebug";
import { GitFork, Pause, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { BreakpointDialog, ForkDialog, StartedNote } from "./debugDialogs";
import { ReplayBadge } from "./replayBadge";

/**
 * The side panel of the trace: the selected step's fields and, for a model call, the exact request
 * and response the gateway stored, with Fork from this step and Pause here on a rerun.
 */
export function StepInspector({
	runId,
	step,
	call,
	canFork,
	canBreak = true,
	onClose,
}: {
	runId: string;
	step: StepRecord | null;
	call?: RunCall;
	canFork: boolean;
	/** N212: Pause here needs a server that can hold model calls. */
	canBreak?: boolean;
	onClose(): void;
}) {
	const { t } = useTranslation("runDebug");
	const [forkOpen, setForkOpen] = useState(false);
	const [bpOpen, setBpOpen] = useState(false);
	// N214: the dialog closes when its run starts; the result line stays here, under the buttons.
	const [started, setStarted] = useState<DebugRunResponse | null>(null);
	if (!step) {
		return (
			<aside className="text-muted-foreground rounded-xl border p-4 text-xs" data-testid="step-inspector-empty">
				{t("inspector.empty")}
			</aside>
		);
	}
	const io = stepIO(step);
	return (
		<aside
			className="flex min-w-0 flex-col gap-3 rounded-xl border p-4"
			data-testid="step-inspector"
			data-seq={step.seq}
			aria-label={t("inspector.title", { seq: step.seq })}
		>
			<div className="flex items-start gap-2">
				<div className="flex min-w-0 flex-1 flex-col gap-0.5">
					<h3 className="text-sm font-semibold">{t("inspector.title", { seq: step.seq })}</h3>
					<span className="text-muted-foreground flex flex-wrap items-center gap-2 text-xs">
						<span className="font-mono">{step.kind}</span>
						{step.tool_name && <span className="font-mono">{clipText(step.tool_name, 60)}</span>}
						{call && <span>{t("inspector.call", { call: call.call })}</span>}
						{call?.replay && <ReplayBadge label={call.replay} />}
					</span>
				</div>
				<button
					type="button"
					onClick={onClose}
					className="text-muted-foreground hover:bg-muted hover:text-foreground rounded-md p-1"
					aria-label={t("inspector.close")}
				>
					<X className="size-4" aria-hidden="true" />
				</button>
			</div>
			{step.summary && <p className="text-xs break-words">{clipText(step.summary, 600)}</p>}
			{call && (
				<div className="flex flex-wrap gap-2">
					{canFork && (
						<Button size="sm" variant="outline" onClick={() => setForkOpen(true)} dataTestId="inspector-fork">
							<GitFork aria-hidden="true" />
							{t("inspector.fork")}
						</Button>
					)}
					{canBreak && (
						<Button size="sm" variant="outline" onClick={() => setBpOpen(true)} dataTestId="inspector-breakpoint">
							<Pause aria-hidden="true" />
							{t("inspector.breakpoint")}
						</Button>
					)}
				</div>
			)}
			<StartedNote started={started} />
			{step.request_id ? (
				<RawCall runId={runId} requestId={step.request_id} />
			) : (
				<div className="grid min-w-0 gap-3">
					<JsonBlock title={t("inspector.input")} value={io.input} />
					<JsonBlock title={t("inspector.output")} value={io.output} />
				</div>
			)}
			<p className="text-muted-foreground text-[11px]">{t("inspector.keys")}</p>
			{forkOpen && call && <ForkDialog runId={runId} call={call.call} open onClose={() => setForkOpen(false)} onStarted={setStarted} />}
			{bpOpen && call && (
				<BreakpointDialog runId={runId} open initialCalls={String(call.call)} onClose={() => setBpOpen(false)} onStarted={setStarted} />
			)}
		</aside>
	);
}

function RawCall({ runId, requestId }: { runId: string; requestId: string }) {
	const { t } = useTranslation("runDebug");
	const q = useGetRunCallQuery({ runId, requestId });
	if (q.isLoading) return <div className="bg-muted/40 h-32 animate-pulse rounded-lg" data-testid="inspector-raw-loading" />;
	if (q.error || !q.data) {
		return (
			<p className="text-chart-error-ink text-xs" data-testid="inspector-raw-error">
				{t("inspector.loadError")}
			</p>
		);
	}
	const d = q.data;
	if (!d.available) {
		return (
			<p className="text-muted-foreground text-xs" data-testid="inspector-raw-missing">
				{t("inspector.noRaw")}
			</p>
		);
	}
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid="inspector-raw">
			<span className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">{t("inspector.raw")}</span>
			<RawBlock title={t("inspector.request")} text={d.request} testId="inspector-request" />
			<RawBlock title={t("inspector.response")} text={d.response} testId="inspector-response" />
			{d.clipped && <p className="text-muted-foreground text-[11px]">{t("inspector.clipped")}</p>}
		</div>
	);
}

function RawBlock({ title, text, testId }: { title: string; text: string; testId: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<span className="text-muted-foreground text-[11px]">{title}</span>
			<pre
				className="bg-muted/40 max-h-80 overflow-auto rounded-lg border p-3 font-mono text-[11px] leading-relaxed break-all whitespace-pre-wrap"
				data-testid={testId}
			>
				{text}
			</pre>
		</div>
	);
}