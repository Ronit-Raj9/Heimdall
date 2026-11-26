import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { withOrgParam } from "@/lib/store/apis/orgParam";
import { useGetRunRecordingsQuery, type RecordingView } from "@/lib/store/apis/recordingsApi";
import { cn } from "@/lib/utils";
import { Columns2, Download, Film, Pause, Play, UserRound, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { activeStepIndex, formatClock, formatSize, otherRecordings, playableVideos, stepMarks } from "./recordingPlayer.utils";

/** Recordings of a run: GET /runs/{id}/recordings. */
export function useRunRecordings(runId: string, skip = false) {
	return useGetRunRecordingsQuery(runId, { skip });
}

/**
 * The recording of one run (lane Z03): the browser or desktop video beside the steps of the
 * service that was recorded. Clicking a step jumps the video to it; while the video plays, the
 * step under the playhead is highlighted and the marks on the bar show where each step falls.
 * onVideo hands the video element to a parent that plays two runs together.
 */
export function RecordingPlayer({
	runId,
	compact = false,
	onVideo,
	label,
}: {
	runId: string;
	compact?: boolean;
	onVideo?: (el: HTMLVideoElement | null) => void;
	label?: string;
}) {
	const { t } = useTranslation("recordings");
	const q = useRunRecordings(runId);
	const videos = useMemo(() => playableVideos(q.data?.recordings ?? []), [q.data]);
	const files = useMemo(() => otherRecordings(q.data?.recordings ?? []), [q.data]);
	const [pick, setPick] = useState(0);
	const [now, setNow] = useState(0);
	const [duration, setDuration] = useState(0);
	const ref = useRef<HTMLVideoElement>(null);
	const video: RecordingView | undefined = videos[Math.min(pick, Math.max(videos.length - 1, 0))];
	const marks = useMemo(() => (video ? stepMarks(q.data?.steps ?? [], video.service, video.started_at) : []), [q.data, video]);
	const active = activeStepIndex(marks, now);
	const list = useRef<HTMLOListElement>(null);

	useEffect(() => {
		onVideo?.(ref.current);
		return () => onVideo?.(null);
	}, [onVideo, video?.url]);

	useEffect(() => {
		const el = list.current?.querySelector<HTMLElement>(`[data-step="${active}"]`);
		el?.scrollIntoView?.({ block: "nearest" });
	}, [active]);

	if (q.isLoading) return <Skeleton className="aspect-video w-full rounded-xl" data-testid="recording-loading" />;
	if (q.error)
		return (
			<p className="text-destructive text-sm" data-testid="recording-error">
				{t("loadError")}
			</p>
		);
	if (!video) {
		return (
			<div className="flex flex-col items-center gap-3 py-12 text-center" data-testid="recording-empty">
				<span className="bg-muted flex size-12 items-center justify-center rounded-full">
					<Film className="text-muted-foreground size-6" aria-hidden="true" />
				</span>
				<p className="text-muted-foreground max-w-md text-sm">{t("empty")}</p>
				{files.length > 0 && <RecordingFiles files={files} />}
			</div>
		);
	}

	const jump = (ms: number) => {
		const el = ref.current;
		if (!el) return;
		el.currentTime = ms / 1000;
		setNow(ms);
		void el.play?.()?.catch?.(() => {});
	};
	const total = Math.max(duration * 1000, marks.length ? marks[marks.length - 1].offsetMs + 1000 : 1);

	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="recording-player" data-run-id={runId}>
			{label && <p className="text-sm font-medium">{label}</p>}
			{q.data?.human_assisted && (
				<p
					className="bg-chart-warning/10 text-chart-warning-ink flex items-center gap-2 rounded-md px-3 py-2 text-xs"
					data-testid="recording-human-assisted"
				>
					<UserRound className="size-4" aria-hidden="true" />
					{t("humanAssistedNote")}
				</p>
			)}
			<div className={cn("flex min-w-0 flex-col gap-4", !compact && "lg:flex-row")}>
				<div className="flex min-w-0 flex-1 flex-col gap-2">
					<video
						key={video.url}
						ref={ref}
						src={withOrgParam(video.url ?? "")}
						controls
						preload="metadata"
						className="bg-muted block aspect-video w-full rounded-xl border object-contain"
						onTimeUpdate={(e) => setNow(e.currentTarget.currentTime * 1000)}
						onLoadedMetadata={(e) => setDuration(Number.isFinite(e.currentTarget.duration) ? e.currentTarget.duration : 0)}
						data-testid="recording-video"
					/>
					<div className="bg-muted relative h-2 rounded-full" aria-hidden="true" data-testid="recording-marks">
						<span
							className="bg-primary/30 absolute inset-y-0 left-0 rounded-full"
							style={{ width: `${Math.min(100, (now / total) * 100)}%` }}
						/>
						{marks.map((m, i) => (
							<span
								key={m.step.seq}
								className={cn("absolute top-0 h-2 w-0.5", m.step.is_error ? "bg-destructive" : "bg-primary", i === active && "w-1")}
								style={{ left: `${Math.min(100, (m.offsetMs / total) * 100)}%` }}
							/>
						))}
					</div>
					<div className="text-muted-foreground flex flex-wrap items-center gap-2 text-xs">
						{videos.length > 1 &&
							videos.map((v, i) => (
								<Button
									key={v.index}
									size="sm"
									variant={i === pick ? "default" : "outline"}
									onClick={() => setPick(i)}
									data-testid={`recording-pick-${i}`}
								>
									{t("video", { n: i + 1 })}
								</Button>
							))}
						<span>{video.started_at ? t("started", { time: new Date(video.started_at).toLocaleTimeString() }) : t("noStart")}</span>
						<span className="ml-auto tabular-nums">{formatSize(video.bytes)}</span>
						<Button size="sm" variant="ghost" asChild>
							<a href={withOrgParam(video.url ?? "")} download={video.file} aria-label={t("download")}>
								<Download aria-hidden="true" />
							</a>
						</Button>
					</div>
				</div>
				<div className={cn("flex flex-col gap-2", !compact && "lg:w-80")}>
					<p className="text-sm font-medium">{t("steps")}</p>
					{marks.length === 0 ? (
						<p className="text-muted-foreground text-xs">{t("noSteps")}</p>
					) : (
						<ol
							ref={list}
							className="flex max-h-[28rem] flex-col gap-1 overflow-y-auto"
							aria-label={t("steps")}
							data-testid="recording-steps"
						>
							{marks.map((m, i) => (
								<li key={m.step.seq} data-step={i}>
									<button
										type="button"
										onClick={() => jump(m.offsetMs)}
										aria-current={i === active ? "step" : undefined}
										className={cn(
											"hover:bg-muted flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm",
											i === active && "bg-muted font-medium",
										)}
										data-testid={`recording-step-${i}`}
									>
										<span className="text-muted-foreground w-10 shrink-0 text-xs tabular-nums">{formatClock(m.offsetMs)}</span>
										<span className="min-w-0 flex-1 truncate" title={m.step.label}>
											{m.step.label}
										</span>
										{m.step.is_error && <span className="bg-destructive mt-1.5 size-1.5 shrink-0 rounded-full" aria-label={t("faulted")} />}
									</button>
								</li>
							))}
						</ol>
					)}
				</div>
			</div>
			{files.length > 0 && <RecordingFiles files={files} />}
			{q.data?.actions && q.data.actions.length > 0 && (
				<div className="flex flex-col gap-1" data-testid="recording-actions">
					<p className="text-sm font-medium">{t("actions")}</p>
					<ul className="text-muted-foreground flex flex-col gap-0.5 text-xs">
						{q.data.actions.map((a) => (
							<li key={a.seq} title={a.by || undefined}>
								{t("actionRow", {
									action: a.action,
									detail: a.text ?? a.combo ?? a.direction ?? (a.x !== undefined ? `(${a.x}, ${a.y})` : ""),
									by: a.by_name || a.by || "?",
								})}
								{a.error ? ` (${a.error})` : ""}
							</li>
						))}
					</ul>
				</div>
			)}
		</div>
	);
}

/** The HAR, console and failed recordings, as downloads or reasons. */
function RecordingFiles({ files }: { files: RecordingView[] }) {
	const { t } = useTranslation("recordings");
	return (
		<div className="flex flex-col gap-1 text-left" data-testid="recording-files">
			<p className="text-sm font-medium">{t("files")}</p>
			<ul className="flex flex-col gap-1 text-xs">
				{files.map((f) => (
					<li key={f.index} className="flex items-center gap-2">
						<span className="font-mono">
							{f.service}/{f.id}
						</span>
						<span className="text-muted-foreground">{f.kind}</span>
						{f.url && !f.error ? (
							<a className="text-primary underline-offset-2 hover:underline" href={withOrgParam(f.url)} download={f.file}>
								{t("download")} {formatSize(f.bytes)}
							</a>
						) : (
							<span className="text-muted-foreground">{t("failed", { error: f.error ?? "" })}</span>
						)}
					</li>
				))}
			</ul>
		</div>
	);
}

/**
 * The Recording tab: this run's player and, on request, a second run beside it (an agent's two
 * versions on one task, or a pass next to a fail), with one button that plays both from the start.
 */
export function RecordingTab({ runId }: { runId: string }) {
	const { t } = useTranslation("recordings");
	const [other, setOther] = useState("");
	const [shown, setShown] = useState<string | null>(null);
	const a = useRef<HTMLVideoElement | null>(null);
	const b = useRef<HTMLVideoElement | null>(null);
	const [playing, setPlaying] = useState(false);
	const playBoth = () => {
		const els = [a.current, b.current].filter((el): el is HTMLVideoElement => Boolean(el));
		if (playing) {
			els.forEach((el) => el.pause());
			setPlaying(false);
			return;
		}
		els.forEach((el) => {
			el.currentTime = 0;
			void el.play?.()?.catch?.(() => {});
		});
		setPlaying(true);
	};
	return (
		<div className="flex flex-col gap-6" data-testid="recording-tab">
			<div className="flex flex-col gap-1">
				<p className="text-muted-foreground text-sm">{t("subtitle")}</p>
			</div>
			{shown ? (
				<div className="flex flex-col gap-3" data-testid="recording-compare">
					<div className="flex flex-wrap items-center gap-2">
						<Button size="sm" onClick={playBoth} data-testid="recording-play-both">
							{playing ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}
							{playing ? t("compare.pauseBoth") : t("compare.playBoth")}
						</Button>
						<Button size="sm" variant="ghost" onClick={() => setShown(null)} data-testid="recording-compare-close">
							<X aria-hidden="true" />
							{t("compare.close")}
						</Button>
					</div>
					<div className="grid min-w-0 gap-4 xl:grid-cols-2">
						<RecordingPlayer runId={runId} compact label={t("compare.thisRun")} onVideo={(el) => (a.current = el)} />
						<RecordingPlayer runId={shown} compact label={t("compare.other", { id: shown })} onVideo={(el) => (b.current = el)} />
					</div>
				</div>
			) : (
				<RecordingPlayer runId={runId} />
			)}
			<form
				className="flex flex-col gap-2 rounded-lg border p-3"
				onSubmit={(e) => {
					e.preventDefault();
					const id = other.trim();
					if (id && id !== runId) {
						setShown(id);
						setPlaying(false);
					}
				}}
				data-testid="recording-compare-form"
			>
				<p className="flex items-center gap-2 text-sm font-medium">
					<Columns2 className="size-4" aria-hidden="true" />
					{t("compare.title")}
				</p>
				<p className="text-muted-foreground text-xs">{t("compare.hint")}</p>
				<div className="flex flex-wrap gap-2">
					<Input
						value={other}
						onChange={(e) => setOther(e.target.value)}
						placeholder="r_..."
						aria-label={t("compare.otherRun")}
						className="max-w-xs"
						data-testid="recording-compare-input"
					/>
					<Button
						type="submit"
						size="sm"
						variant="outline"
						disabled={!other.trim() || other.trim() === runId}
						data-testid="recording-compare-show"
					>
						{t("compare.show")}
					</Button>
				</div>
			</form>
		</div>
	);
}