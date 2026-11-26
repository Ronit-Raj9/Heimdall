import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { frameImageUrl, useGetRunFramesQuery, type ReplayFrame } from "@/lib/store/apis/framesApi";
import { cn } from "@/lib/utils";
import { ChevronLeft, ChevronRight, Download, Maximize, MonitorPlay, Pause, Play } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

const SPEEDS = [1, 2, 4] as const;
/** Base time a frame stays on screen at 1x when the recorded gap is shorter or missing. */
const MIN_FRAME_MS = 600;
const MAX_FRAME_MS = 3000;

/** How long frame i stays on screen at speed: its recorded gap to the next frame, clamped. */
export function frameDelay(frames: ReplayFrame[], i: number, speed: number): number {
	const next = frames[i + 1];
	const gap = next ? next.offset_ms - frames[i].offset_ms : MIN_FRAME_MS;
	return Math.min(Math.max(gap, MIN_FRAME_MS), MAX_FRAME_MS) / speed;
}

/** m:ss of a replay offset. */
export function formatOffset(ms: number): string {
	const s = Math.max(0, Math.floor(ms / 1000));
	return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** The step label: the acting tool call before the frame, short. */
export function actionLabel(f: ReplayFrame, fallback: string): string {
	return f.action?.trim() || fallback;
}

/** Frames of a run: GET /runs/{id}/frames, polled every 3 s while the run is live. */
export function useRunFrames(runId: string, live: boolean) {
	return useGetRunFramesQuery(runId, { pollingInterval: live ? 3000 : 0 });
}

/**
 * Screen replay of a computer-use or browser run: the screenshots the agent got, played like a
 * video, with a scrubber marked at each step and the step list beside it.
 */
/** Play or pause; play on the last frame starts again from the first. The button and Space both use it. */
export function playToggle(playing: boolean, index: number, last: number): { playing: boolean; index: number } {
	if (!playing && index >= last) return { playing: true, index: 0 };
	return { playing: !playing, index };
}

export function ScreenReplay({ runId, live = false }: { runId: string; live?: boolean }) {
	const { t } = useTranslation("replay");
	const q = useRunFrames(runId, live);
	const frames = q.data?.frames ?? [];
	const [cur, setCur] = useState(0);
	const [playing, setPlaying] = useState(false);
	const [speed, setSpeed] = useState<(typeof SPEEDS)[number]>(1);
	const stage = useRef<HTMLDivElement>(null);
	const last = frames.length - 1;
	const index = Math.min(cur, Math.max(last, 0));

	useEffect(() => {
		if (!playing || frames.length === 0) return;
		if (index >= last) {
			setPlaying(false);
			return;
		}
		const id = window.setTimeout(() => setCur(index + 1), frameDelay(frames, index, speed));
		return () => window.clearTimeout(id);
	}, [playing, index, last, frames, speed]);

	const step = useCallback((d: number) => setCur((c) => Math.min(Math.max(c + d, 0), Math.max(last, 0))), [last]);

	const toggle = () => {
		const next = playToggle(playing, index, last);
		setCur(next.index);
		setPlaying(next.playing);
	};

	const onKey = (e: React.KeyboardEvent) => {
		if (e.key === "ArrowRight") step(1);
		else if (e.key === "ArrowLeft") step(-1);
		else if (e.key === " ") toggle();
		else return;
		e.preventDefault();
	};

	if (q.isLoading) return <Skeleton className="aspect-video w-full rounded-xl" data-testid="replay-loading" />;
	if (q.error) return <p className="text-destructive text-sm" data-testid="replay-error">{t("loadError")}</p>;
	if (frames.length === 0) {
		return (
			<div className="flex flex-col items-center gap-3 py-12 text-center" data-testid="replay-empty">
				<span className="bg-muted flex size-12 items-center justify-center rounded-full">
					<MonitorPlay className="text-muted-foreground size-6" aria-hidden="true" />
				</span>
				<p className="text-muted-foreground text-sm">{t("empty")}</p>
			</div>
		);
	}

	const f = frames[index];
	const total = Math.max(frames[last].offset_ms, 1);
	const src = frameImageUrl(runId, f.index);

	return (
		<div className="flex min-w-0 flex-col gap-4 lg:flex-row" data-testid="screen-replay" tabIndex={0} onKeyDown={onKey} aria-label={t("title")}>
			<div className="flex min-w-0 flex-1 flex-col gap-3">
				<div ref={stage} className="bg-muted relative overflow-hidden rounded-xl border">
					<img src={src} alt={t("frameOf", { n: index + 1, total: frames.length })} className="block h-auto w-full object-contain" data-testid="replay-frame" />
					{live && (
						<span className="bg-background/80 absolute top-3 left-3 flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs">
							<span className="size-2 animate-pulse rounded-full bg-blue-500" aria-hidden="true" />
							{t("live")}
						</span>
					)}
				</div>
				<div className="relative h-6" data-testid="replay-scrubber-wrap">
					<input
						type="range"
						min={0}
						max={last}
						value={index}
						onChange={(e) => setCur(Number(e.target.value))}
						aria-label={t("scrubber")}
						className="accent-primary absolute inset-0 w-full"
						data-testid="replay-scrubber"
					/>
					<div className="pointer-events-none absolute inset-x-0 top-0 h-1.5" aria-hidden="true">
						{frames.map((fr) =>
							fr.action ? (
								<span key={fr.index} className="bg-primary/60 absolute top-0 h-1.5 w-0.5" style={{ left: `${(fr.offset_ms / total) * 100}%` }} />
							) : null,
						)}
					</div>
				</div>
				<div className="flex flex-wrap items-center gap-2">
					<Button size="sm" variant="ghost" onClick={() => step(-1)} aria-label={t("prev")} data-testid="replay-prev">
						<ChevronLeft aria-hidden="true" />
					</Button>
					<Button
						size="sm"
						onClick={toggle}
						aria-label={playing ? t("pause") : t("play")}
						data-testid="replay-play"
					>
						{playing ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}
					</Button>
					<Button size="sm" variant="ghost" onClick={() => step(1)} aria-label={t("next")} data-testid="replay-next">
						<ChevronRight aria-hidden="true" />
					</Button>
					<div className="flex rounded-md border" role="group" aria-label={t("speed")}>
						{SPEEDS.map((s) => (
							<button
								key={s}
								type="button"
								onClick={() => setSpeed(s)}
								aria-pressed={speed === s}
								className={cn("px-2 py-1 text-xs", speed === s ? "bg-primary text-primary-foreground" : "text-muted-foreground")}
								data-testid={`replay-speed-${s}`}
							>
								{s}x
							</button>
						))}
					</div>
					<span className="text-muted-foreground ml-auto text-xs tabular-nums" data-testid="replay-position">
						{formatOffset(f.offset_ms)} / {formatOffset(frames[last].offset_ms)} · {t("frameOf", { n: index + 1, total: frames.length })}
					</span>
					<Button size="sm" variant="ghost" asChild>
						<a href={src} download={`frame-${String(index + 1).padStart(4, "0")}`} aria-label={t("download")}>
							<Download aria-hidden="true" />
						</a>
					</Button>
					<Button size="sm" variant="ghost" onClick={() => void stage.current?.requestFullscreen?.()} aria-label={t("fullscreen")}>
						<Maximize aria-hidden="true" />
					</Button>
				</div>
			</div>
			<ol className="flex max-h-[32rem] flex-col gap-1 overflow-y-auto lg:w-72" aria-label={t("steps")} data-testid="replay-steps">
				{frames.map((fr, i) => (
					<li key={fr.index}>
						<button
							type="button"
							onClick={() => {
								setPlaying(false);
								setCur(i);
							}}
							aria-current={i === index ? "step" : undefined}
							className={cn("hover:bg-muted flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm", i === index && "bg-muted font-medium")}
						>
							<span className="text-muted-foreground w-10 shrink-0 text-xs tabular-nums">{formatOffset(fr.offset_ms)}</span>
							<span className="truncate" title={actionLabel(fr, t("look"))}>
								{actionLabel(fr, t("look"))}
							</span>
						</button>
					</li>
				))}
			</ol>
		</div>
	);
}
