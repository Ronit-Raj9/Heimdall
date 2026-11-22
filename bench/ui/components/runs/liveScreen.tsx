import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { apiErrorMessage } from "@/lib/store/apis/integrationsApi";
import { liveFrameUrl, useGetRunLiveQuery, useTakeoverRunMutation, type TakeoverInput } from "@/lib/store/apis/recordingsApi";
import { cn } from "@/lib/utils";
import { ArrowDown, ArrowUp, Hand, MonitorPlay } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { framePoint } from "./recordingPlayer.utils";

/**
 * The live screen of a running browser or desktop run (lane Z03): the newest frame the run
 * published, polled every 2 s, display only. In a debug run whose service takes input, a member
 * may take control: a click on the frame clicks there, and the text box and keys type. Each input
 * is recorded in the run and marks it human assisted, which keeps it out of every verdict.
 * Renders nothing for a run that never showed its screen.
 */
export function LiveScreen({ runId, active = true }: { runId: string; active?: boolean }) {
	const { t } = useTranslation("recordings");
	const q = useGetRunLiveQuery(runId, { pollingInterval: active ? 2000 : 0, skip: !active, skipPollingIfUnfocused: true });
	const [control, setControl] = useState(false);
	const [text, setText] = useState("");
	const [note, setNote] = useState<string | null>(null);
	const [send, sending] = useTakeoverRunMutation();
	const img = useRef<HTMLImageElement>(null);
	const s = q.data;
	if (!s?.live) return null;
	const go = async (input: TakeoverInput) => {
		setNote(null);
		try {
			await send({ runId, input }).unwrap();
			setNote(t("live.sent"));
		} catch (e) {
			setNote(t("live.sendError", { error: apiErrorMessage(e, String((e as { status?: unknown })?.status ?? "")) }));
		}
	};
	const onClick = (e: React.MouseEvent<HTMLImageElement>) => {
		if (!control || !img.current) return;
		const el = img.current;
		const box = el.getBoundingClientRect();
		const p = framePoint(e.clientX, e.clientY, box, { width: el.naturalWidth, height: el.naturalHeight });
		if (p) void go({ action: "click", x: p.x, y: p.y, frame_w: el.naturalWidth, frame_h: el.naturalHeight });
	};
	return (
		<section className="flex flex-col gap-3 rounded-xl border p-4" data-testid="live-screen" aria-label={t("live.title")}>
			<div className="flex flex-wrap items-center gap-2">
				<MonitorPlay className="text-chart-info-ink size-4" aria-hidden="true" />
				<h3 className="text-sm font-medium">{t("live.title")}</h3>
				<span className="bg-chart-info size-1.5 animate-pulse rounded-full" aria-hidden="true" />
				<span className="text-muted-foreground text-xs">{s.at ? t("live.updated", { time: new Date(s.at).toLocaleTimeString() }) : ""}</span>
				{s.takeover_ok ? (
					<Button
						size="sm"
						variant={control ? "default" : "outline"}
						className="ml-auto"
						onClick={() => setControl((c) => !c)}
						aria-pressed={control}
						data-testid="live-take-control"
					>
						<Hand aria-hidden="true" />
						{control ? t("live.release") : t("live.takeControl")}
					</Button>
				) : (
					<span className="text-muted-foreground ml-auto text-xs" data-testid="live-debug-only">
						{t("live.debugOnly")}
					</span>
				)}
			</div>
			<p className="text-muted-foreground text-xs">{t("live.subtitle")}</p>
			<img
				ref={img}
				src={liveFrameUrl(runId, s.at)}
				alt={t("live.alt")}
				onClick={onClick}
				className={cn("bg-muted block h-auto w-full rounded-lg border object-contain", control && "cursor-crosshair ring-2 ring-primary")}
				data-testid="live-frame"
			/>
			{control && (
				<div className="flex flex-col gap-2" data-testid="live-controls">
					<p className="bg-chart-warning/10 text-chart-warning-ink rounded-md px-3 py-2 text-xs">{t("live.takeoverNote")}</p>
					<p className="text-muted-foreground text-xs">{t("live.clickHint")}</p>
					<form
						className="flex flex-wrap gap-2"
						onSubmit={(e) => {
							e.preventDefault();
							if (text) void go({ action: "type", text }).then(() => setText(""));
						}}
					>
						<Input value={text} onChange={(e) => setText(e.target.value)} placeholder={t("live.text")} aria-label={t("live.text")} className="max-w-sm" data-testid="live-text" />
						<Button type="submit" size="sm" disabled={!text || sending.isLoading} data-testid="live-send-text">
							{t("live.sendText")}
						</Button>
					</form>
					<div className="flex flex-wrap gap-2">
						{(
							[
								["Enter", "live.enter"],
								["Tab", "live.tab"],
								["Escape", "live.escape"],
							] as const
						).map(([combo, label]) => (
							<Button key={combo} size="sm" variant="outline" onClick={() => void go({ action: "key", combo })} data-testid={`live-key-${combo}`}>
								{t(label)}
							</Button>
						))}
						<Button size="sm" variant="outline" onClick={() => void go({ action: "scroll", direction: "up" })} data-testid="live-scroll-up">
							<ArrowUp aria-hidden="true" />
							{t("live.scrollUp")}
						</Button>
						<Button size="sm" variant="outline" onClick={() => void go({ action: "scroll", direction: "down" })} data-testid="live-scroll-down">
							<ArrowDown aria-hidden="true" />
							{t("live.scrollDown")}
						</Button>
						{s.pending ? <span className="text-muted-foreground self-center text-xs">{t("live.pending", { count: s.pending })}</span> : null}
					</div>
					{note && (
						<p className="text-muted-foreground text-xs" role="status" data-testid="live-note">
							{note}
						</p>
					)}
				</div>
			)}
		</section>
	);
}
