import { clipText } from "@/components/runs/stepTimeline.utils";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { type RunVisual, type VisualImageName, useGetRunVisualQuery, visualImageUrl } from "@/lib/store/apis/visualApi";
import type { VisualCheckResult, VisualGrade, VisualImage, VisualJudgeResult, VisualRect } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

// X1 vision grading (docs/visual-grading.md): the visual panel of the run page.

/**
 * One run's visual grade, fetched by run id. Renders nothing for a run graded without output.visual,
 * while loading, or on an error: the run detail beside it reports those states.
 */
export function RunVisualPanel({ runId }: { runId: string }) {
	const q = useGetRunVisualQuery(runId);
	if (!q.data?.visual) return null;
	return <VisualPanel data={q.data} />;
}

/** Format a check value: integers as they are, others to at most six decimals. */
export function formatVisualValue(v: number): string {
	if (!Number.isFinite(v)) return String(v);
	return Number.isInteger(v) ? String(v) : String(Number(v.toFixed(6)));
}

/** A mask as a percentage box over an image of width x height. */
export function maskStyle(m: VisualRect, width: number, height: number): React.CSSProperties {
	const pct = (n: number, of: number) => `${of > 0 ? (n / of) * 100 : 0}%`;
	return { left: pct(m.x, width), top: pct(m.y, height), width: pct(m.w, width), height: pct(m.h, height) };
}

/**
 * The capture the bench took, the hidden reference it was compared with and the diff heatmap, the
 * masks the checks ignored, every check's number, and the vision judge's report when there is one.
 */
export function VisualPanel({ data }: { data: RunVisual }) {
	const { t } = useTranslation("bench");
	const v = data.visual as VisualGrade;
	const checks = v.checks ?? [];
	const src = v.source;
	return (
		<div className="flex min-w-0 flex-col gap-3 rounded-md border p-3 text-xs" data-testid={`visual-panel-${data.runId}`}>
			<div className="flex flex-wrap items-center gap-2">
				<p className="text-sm font-medium">{t("visual.title")}</p>
				<ToneBadge tone={checks.length > 0 && v.passed === checks.length ? "good" : "bad"} testId="visual-checks-badge">
					{t("visual.checksPassed", { passed: v.passed, total: checks.length })}
				</ToneBadge>
				<ToneBadge tone="neutral">{t(`visual.kind.${src.kind}`, { defaultValue: src.kind })}</ToneBadge>
				{v.judge ? (
					<ToneBadge tone="warn" hint={t("visual.judgedHint")} testId="visual-judged-badge">
						{t("visual.judged")}
					</ToneBadge>
				) : null}
			</div>
			<SourceLine grade={v} />
			{data.sealed ? (
				<div className="grid gap-3 md:grid-cols-3">
					<Figure runId={data.runId} name="capture.png" image={v.capture} caption={t("visual.capture")} masks={v.masks} />
					{v.reference ? (
						<Figure runId={data.runId} name="reference.png" image={v.reference} caption={t("visual.reference")} masks={v.masks} />
					) : null}
					{v.diff ? <Figure runId={data.runId} name="diff.png" image={v.diff} caption={t("visual.diff")} /> : null}
				</div>
			) : (
				<p className="text-muted-foreground" data-testid="visual-unsealed">
					{t("visual.unsealed")}
				</p>
			)}
			{v.masks && v.masks.length > 0 ? (
				<p className="text-muted-foreground" data-testid="visual-masks">
					{t("visual.masks", { list: v.masks.map((m) => `${m.w}x${m.h} at (${m.x},${m.y})`).join(", ") })}
				</p>
			) : null}
			<ChecksTable checks={checks} />
			{v.judge ? <JudgeBlock judge={v.judge} /> : null}
		</div>
	);
}

/** How the capture was taken, in one line. */
function SourceLine({ grade: v }: { grade: VisualGrade }) {
	const { t } = useTranslation("bench");
	const s = v.source;
	const parts = [t("visual.size", { w: v.capture.width, h: v.capture.height })];
	if (s.url) parts.push(clipText(s.url, 120));
	if (s.artifact) parts.push(clipText(s.artifact, 120));
	if (s.service) parts.push(t("visual.service", { id: clipText(s.service, 60) }));
	if (s.scale) parts.push(t("visual.scale", { scale: s.scale }));
	if (s.full_page) parts.push(t("visual.fullPage"));
	if (s.renderer) parts.push(t("visual.renderer", { image: clipText(s.renderer, 120) }));
	if (s.fonts_sha256) parts.push(t("visual.fonts", { sha: s.fonts_sha256.slice(0, 12) }));
	if (s.pointer_parked) parts.push(t("visual.pointerParked"));
	return (
		<p className="text-muted-foreground" data-testid="visual-source">
			{parts.join(" · ")}
		</p>
	);
}

function Figure({
	runId,
	name,
	image,
	caption,
	masks,
}: {
	runId: string;
	name: VisualImageName;
	image: VisualImage;
	caption: string;
	masks?: VisualRect[];
}) {
	return (
		<figure className="flex min-w-0 flex-col gap-1" data-testid={`visual-figure-${name}`}>
			<div className="bg-muted relative w-full overflow-hidden rounded border">
				<img src={visualImageUrl(runId, name)} alt={caption} className="block h-auto w-full" loading="lazy" />
				{(masks ?? []).map((m, i) => (
					<div
						key={i}
						className="pointer-events-none absolute border border-blue-500 bg-blue-500/20"
						style={maskStyle(m, image.width, image.height)}
						data-testid={`visual-mask-${name}-${i}`}
						aria-hidden="true"
					/>
				))}
			</div>
			<figcaption className="text-muted-foreground">
				{caption}: {image.width}x{image.height}, sha256 {image.sha256.slice(0, 12)}
			</figcaption>
		</figure>
	);
}

function ChecksTable({ checks }: { checks: VisualCheckResult[] }) {
	const { t } = useTranslation("bench");
	if (checks.length === 0) return <p className="text-muted-foreground">{t("visual.noChecks")}</p>;
	return (
		<div className="overflow-x-auto">
			<table className="w-full text-left" data-testid="visual-checks">
				<thead className="text-muted-foreground">
					<tr>
						<th className="py-1 pr-2 font-medium">{t("visual.col.check")}</th>
						<th className="py-1 pr-2 font-medium">{t("visual.col.method")}</th>
						<th className="py-1 pr-2 font-medium">{t("visual.col.value")}</th>
						<th className="py-1 pr-2 font-medium">{t("visual.col.threshold")}</th>
						<th className="py-1 pr-2 font-medium">{t("visual.col.result")}</th>
						<th className="py-1 font-medium">{t("visual.col.detail")}</th>
					</tr>
				</thead>
				<tbody>
					{checks.map((c) => (
						<tr key={c.id} className="border-t align-top" data-testid={`visual-check-${c.id}`}>
							<td className="py-1 pr-2 font-mono">{clipText(c.id, 64)}</td>
							<td className="py-1 pr-2">{c.method}</td>
							<td className="py-1 pr-2 font-mono">{formatVisualValue(c.value)}</td>
							<td className="py-1 pr-2 font-mono">{formatVisualValue(c.threshold)}</td>
							<td className="py-1 pr-2">
								<ToneBadge tone={c.passed ? "good" : "bad"}>{c.passed ? t("visual.pass") : t("visual.fail")}</ToneBadge>
							</td>
							<td className="text-muted-foreground py-1">{clipText(c.detail, 300)}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}

function JudgeBlock({ judge }: { judge: VisualJudgeResult }) {
	const { t } = useTranslation("bench");
	const ag = judge.agreement?.agreement;
	return (
		<div className="flex flex-col gap-1 rounded border p-2" data-testid="visual-judge">
			<div className="flex flex-wrap items-center gap-2">
				<span className="font-medium">{t("visual.judge.title")}</span>
				<ToneBadge tone={judge.verdict === "pass" ? "good" : judge.verdict === "fail" ? "bad" : "warn"}>{judge.verdict}</ToneBadge>
				<ToneBadge
					tone={judge.decides ? "good" : "warn"}
					testId="visual-judge-calibration"
					hint={judge.decides ? undefined : clipText(judge.reason, 300)}
				>
					{judge.decides ? t("visual.judge.calibrated") : t("visual.judge.uncalibrated")}
				</ToneBadge>
			</div>
			<p className="text-muted-foreground">
				{t("visual.judge.line", {
					model: clipText(judge.served_model || judge.model, 80),
					prompt: judge.prompt_version,
					aggregate: judge.aggregate,
					votes: judge.votes.join(", "),
					score: judge.score !== undefined ? formatVisualValue(judge.score) : t("visual.judge.noScore"),
				})}
			</p>
			{judge.decides && judge.calibration ? (
				<p className="text-muted-foreground" data-testid="visual-judge-agreement">
					{t("visual.judge.record", { ref: clipText(judge.calibration, 80) })}
					{ag && ag.value !== null && ag.value !== undefined
						? ` ${t("visual.judge.agreement", { k: ag.k, n: ag.n, value: formatVisualValue(ag.value) })}`
						: ""}
				</p>
			) : null}
			{!judge.decides && judge.reason ? <p className="text-muted-foreground">{clipText(judge.reason, 300)}</p> : null}
			{judge.rationale ? (
				<p data-testid="visual-judge-rationale">
					<span className="text-muted-foreground">{t("visual.judge.rationale")} </span>
					{clipText(judge.rationale, 600)}
				</p>
			) : null}
		</div>
	);
}