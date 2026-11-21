import { Button } from "@/components/ui/button";
import { useGetRunHoldsQuery, useReleaseHoldMutation } from "@/lib/store/apis/runDebugApi";
import type { Hold, HoldAction } from "@/lib/types/runDebug";
import { Pause } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { countdown } from "./debug.utils";
import { errorText } from "./debugDialogs";

/** Poll interval of a live run's breakpoints. */
export const HOLD_POLL_MS = 1000;

/**
 * The breakpoint controls of a live run: the call held now, its pending request, and Continue,
 * Step, Edit answer and Stop run (F8 continues, F10 steps). Renders nothing while no call is held.
 */
export function HoldPanel({ runId }: { runId: string }) {
	const { t } = useTranslation("runDebug");
	const holds = useGetRunHoldsQuery(runId, { pollingInterval: HOLD_POLL_MS, skipPollingIfUnfocused: true });
	const hold: Hold | undefined = holds.data?.holds[0];
	if (holds.data?.stopped && !hold) {
		return (
			<p className="text-muted-foreground rounded-xl border px-4 py-3 text-sm" data-testid="hold-stopped">
				{t("holds.stopped")}
			</p>
		);
	}
	if (!hold) return null;
	return <HoldCard key={`${hold.slot}-${hold.call}`} runId={runId} hold={hold} onDone={() => void holds.refetch()} />;
}

export function HoldCard({ runId, hold, onDone }: { runId: string; hold: Hold; onDone(): void }) {
	const { t } = useTranslation("runDebug");
	const [release, state] = useReleaseHoldMutation();
	const [editing, setEditing] = useState(false);
	const [answer, setAnswer] = useState("");
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const id = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(id);
	}, []);
	const send = async (action: HoldAction, response?: string) => {
		const res = await release({ runId, body: { call: hold.call, action, ...(response ? { response } : {}) } });
		if (!("error" in res)) onDone();
	};
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			if (editing || state.isLoading) return;
			if (e.key === "F8") {
				e.preventDefault();
				void send("continue");
			} else if (e.key === "F10") {
				e.preventDefault();
				void send("step");
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	});
	return (
		<section className="border-chart-warning/40 bg-chart-warning/5 flex flex-col gap-3 rounded-xl border p-4" data-testid="hold-panel" data-call={hold.call} aria-live="polite">
			<div className="flex flex-wrap items-center gap-x-3 gap-y-1">
				<span className="bg-chart-warning/15 text-chart-warning-ink flex size-7 items-center justify-center rounded-md">
					<Pause className="size-3.5" aria-hidden="true" />
				</span>
				<h3 className="text-sm font-semibold">{t("holds.title", { call: hold.call })}</h3>
				<span className="text-muted-foreground text-xs">{t("holds.reason", { reason: hold.reason })}</span>
				<span className="text-muted-foreground ml-auto font-mono text-xs tabular-nums" data-testid="hold-countdown">
					{t("holds.auto", { time: countdown(hold.deadline, now) })}
				</span>
			</div>
			{hold.request && (
				<details className="text-xs">
					<summary className="text-muted-foreground hover:text-foreground cursor-pointer">{t("holds.request")}</summary>
					<pre className="bg-muted/40 mt-2 max-h-64 overflow-auto rounded-lg border p-3 font-mono text-[11px] whitespace-pre-wrap break-all">{hold.request}</pre>
				</details>
			)}
			{editing ? (
				<div className="flex flex-col gap-2">
					<label className="flex flex-col gap-1.5 text-xs font-medium">
						{t("holds.editLabel")}
						<textarea
							className="border-input bg-background focus-visible:ring-ring/50 min-h-20 w-full rounded-lg border px-3 py-2 text-sm focus-visible:ring-[3px] focus-visible:outline-none"
							value={answer}
							onChange={(e) => setAnswer(e.target.value)}
							data-testid="hold-answer"
						/>
					</label>
					<div className="flex gap-2">
						<Button size="sm" onClick={() => void send("edit", answer)} disabled={!answer.trim() || state.isLoading} dataTestId="hold-send">
							{t("holds.send")}
						</Button>
						<Button size="sm" variant="outline" onClick={() => setEditing(false)}>
							{t("holds.cancel")}
						</Button>
					</div>
				</div>
			) : (
				<div className="flex flex-wrap items-center gap-2">
					<Button size="sm" onClick={() => void send("continue")} disabled={state.isLoading} dataTestId="hold-continue">
						{t("holds.continue")}
					</Button>
					<Button size="sm" variant="outline" onClick={() => void send("step")} disabled={state.isLoading} dataTestId="hold-step">
						{t("holds.step")}
					</Button>
					<Button size="sm" variant="outline" onClick={() => setEditing(true)} disabled={state.isLoading} dataTestId="hold-edit">
						{t("holds.edit")}
					</Button>
					<Button size="sm" variant="destructive" onClick={() => void send("abort")} disabled={state.isLoading} dataTestId="hold-abort">
						{t("holds.abort")}
					</Button>
					<span className="text-muted-foreground ml-auto text-xs">{t("holds.keys")}</span>
				</div>
			)}
			{state.error ? (
				<p className="text-chart-error-ink text-xs" role="alert">
					{errorText(state.error) ?? t("holds.failed")}
				</p>
			) : null}
		</section>
	);
}
