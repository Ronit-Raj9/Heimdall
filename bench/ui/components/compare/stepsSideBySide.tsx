import "./ufk2I18n";

import { formatDiffValue, knownNumber } from "@/app/compare/views/runDiff";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useGetRunQuery } from "@/lib/store/apis/benchApi";
import type { PairDivergenceResponse } from "@/lib/store/apis/behaviourApi";
import { useGetRunDivergenceQuery } from "@/lib/store/apis/runDivergenceApi";
import { useGetRunStepsQuery } from "@/lib/store/apis/runStepsApi";
import type { RunDetail, RunSteps, StepRecord } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { GitFork } from "lucide-react";
import { useTranslation } from "react-i18next";

/** One row of the side by side view: a step of each run (null where that run has none), or the divergence marker. */
export type SideRow = { kind: "pair"; a: StepRecord | null; b: StepRecord | null } | { kind: "diverge" };

/** Text longer than this, or with more than 3 lines, is folded. */
export const FOLD_CHARS = 240;

/**
 * Lines the two step lists up: steps before the first divergence are paired by position, then a
 * marker row, then the rest paired by position. seqA or seqB 0 means that side had no step there
 * (it ended). found false or no divergence: everything paired by position, no marker.
 */
export function alignSteps(a: readonly StepRecord[], b: readonly StepRecord[], div: PairDivergenceResponse["divergence"] | undefined): SideRow[] {
	const zip = (x: readonly StepRecord[], y: readonly StepRecord[]): SideRow[] =>
		Array.from({ length: Math.max(x.length, y.length) }, (_, i) => ({ kind: "pair" as const, a: x[i] ?? null, b: y[i] ?? null }));
	if (!div || !div.found) return zip(a, b);
	const cut = (list: readonly StepRecord[], seq: number | undefined) => {
		const i = seq ? list.findIndex((s) => s.seq >= seq) : -1;
		return i < 0 ? list.length : i;
	};
	const ia = cut(a, div.seq_a);
	const ib = cut(b, div.seq_b);
	return [...zip(a.slice(0, ia), b.slice(0, ib)), { kind: "diverge" }, ...zip(a.slice(ia), b.slice(ib))];
}

/** The six measures of one run, as display text: result, steps, tool calls, tokens, cost, time. */
export function runMeasures(d: RunDetail): { success: boolean; steps: string; tools: string; tokens: string; cost: string; time: string } {
	const m = d.metrics as Record<string, Parameters<typeof knownNumber>[0]>;
	return {
		success: d.run.outcome === "pass",
		steps: formatDiffValue("steps", knownNumber(m.steps)),
		tools: formatDiffValue("steps", knownNumber(m.tool_calls)),
		tokens: formatDiffValue("tokens", knownNumber(m.tokens)),
		cost: formatDiffValue("cost", knownNumber(m.cost_usd)),
		time: formatDiffValue("time", knownNumber(m.wall_time_s)),
	};
}

/**
 * Two runs of one task side by side, step by step (model reply, tool call, result), lined up to the
 * first divergence the server computes (GET /runs/{a}/divergence/{b}), with the six measures of
 * each run on top. Two columns from md up, one stacked list below.
 */
export function StepsSideBySide({ a, b, labelA = "A", labelB = "B" }: { a: string; b: string; labelA?: string; labelB?: string }) {
	const { t } = useTranslation("ufk2");
	const ra = useGetRunQuery(a);
	const rb = useGetRunQuery(b);
	const sa = useGetRunStepsQuery(a);
	const sb = useGetRunStepsQuery(b);
	const div = useGetRunDivergenceQuery({ runId: a, other: b });
	for (const [id, q] of [
		[a, ra],
		[b, rb],
		[a, sa],
		[b, sb],
	] as const) {
		if (q.error) return <LoadError title={t("steps.error", { id })} error={q.error} onRetry={() => void q.refetch()} testId="steps-sbs-error" />;
	}
	if (!ra.data || !rb.data || !sa.data || !sb.data || div.isLoading) return <BlockSkeleton rows={6} testId="steps-sbs-loading" />;
	return (
		<StepsSideBySideView
			labels={[labelA, labelB]}
			details={[ra.data, rb.data]}
			steps={[sa.data, sb.data]}
			divergence={div.data}
		/>
	);
}

/** The view of StepsSideBySide from the handlers' JSON; exported for tests. */
export function StepsSideBySideView({
	labels,
	details,
	steps,
	divergence,
}: {
	labels: readonly [string, string];
	details: readonly [RunDetail, RunDetail];
	steps: readonly [RunSteps, RunSteps];
	divergence: PairDivergenceResponse | undefined;
}) {
	const { t } = useTranslation("ufk2");
	const d = divergence?.available ? divergence.divergence : undefined;
	const rows = alignSteps(steps[0].steps ?? [], steps[1].steps ?? [], d);
	const truncated = steps[0].truncated || steps[1].truncated;
	return (
		<section className="flex min-w-0 flex-col gap-3" data-testid="steps-sbs">
			<div className="flex flex-col gap-1">
				<h2 className="text-foreground text-base font-semibold tracking-tight">{t("steps.title")}</h2>
				<p className="text-muted-foreground text-sm">{t("steps.intro")}</p>
			</div>
			<div className="grid min-w-0 gap-3 md:grid-cols-2">
				{details.map((det, i) => (
					<Measures key={labels[i]} label={labels[i]} detail={det} testId={`steps-sbs-measures-${i === 0 ? "a" : "b"}`} />
				))}
			</div>
			{d && !d.found && (
				<p className="text-muted-foreground text-sm" data-testid="steps-sbs-same">
					{t("steps.same")}
				</p>
			)}
			{truncated && <p className="text-muted-foreground text-xs">{t("steps.truncated")}</p>}
			<div className="hidden gap-3 px-1 md:grid md:grid-cols-2" aria-hidden="true">
				<span className="text-sm font-semibold">{labels[0]}</span>
				<span className="text-sm font-semibold">{labels[1]}</span>
			</div>
			<ol className="flex min-w-0 flex-col gap-2" data-testid="steps-sbs-rows">
				{rows.map((row, i) =>
					row.kind === "diverge" ? (
						<li
							key="diverge"
							className="border-chart-warning bg-chart-warning/10 flex flex-col gap-1 rounded-md border-l-2 px-3 py-2"
							data-testid="steps-sbs-diverge"
						>
							<span className="flex items-center gap-2 text-sm font-semibold">
								<GitFork className="text-chart-warning-ink size-4" aria-hidden="true" />
								{t("steps.diverge")}
							</span>
							<span className="text-muted-foreground text-xs">{t("steps.divergeNote")}</span>
						</li>
					) : (
						<li key={`r${i}`} className="grid min-w-0 gap-2 md:grid-cols-2" data-testid="steps-sbs-row">
							<StepCell step={row.a} label={labels[0]} />
							<StepCell step={row.b} label={labels[1]} />
						</li>
					),
				)}
			</ol>
			{rows.length === 0 && <p className="text-muted-foreground text-sm">{t("steps.none")}</p>}
		</section>
	);
}

function Measures({ label, detail, testId }: { label: string; detail: RunDetail; testId: string }) {
	const { t } = useTranslation("ufk2");
	const m = runMeasures(detail);
	const items: [string, string][] = [
		[t("steps.m.steps"), m.steps],
		[t("steps.m.tools"), m.tools],
		[t("steps.m.tokens"), m.tokens],
		[t("steps.m.cost"), m.cost],
		[t("steps.m.time"), m.time],
	];
	return (
		<div className="bg-card min-w-0 rounded-xl border p-3" data-testid={testId}>
			<div className="flex flex-wrap items-center gap-2">
				<span className="text-sm font-semibold">{label}</span>
				<span
					className={cn("rounded-sm px-1.5 py-0.5 text-xs font-medium", m.success ? "bg-chart-success/15 text-chart-success-ink" : "bg-muted text-muted-foreground")}
					data-testid={`${testId}-success`}
				>
					{m.success ? t("steps.solved") : t("steps.notSolved")}
				</span>
			</div>
			<dl className="mt-2 grid grid-cols-3 gap-x-3 gap-y-1 text-xs sm:grid-cols-5">
				{items.map(([k, v]) => (
					<div key={k} className="flex min-w-0 flex-col">
						<dt className="text-muted-foreground">{k}</dt>
						<dd className="tabular-nums">{v}</dd>
					</div>
				))}
			</dl>
		</div>
	);
}

function StepCell({ step, label }: { step: StepRecord | null; label: string }) {
	const { t } = useTranslation("ufk2");
	if (!step) {
		return (
			<div className="text-muted-foreground rounded-md border border-dashed px-3 py-2 text-xs italic">
				<span className="mr-2 font-semibold not-italic md:hidden">{label}</span>
				{t("steps.ended")}
			</div>
		);
	}
	const text = step.summary || step.path || step.tool_name || "";
	const long = text.length > FOLD_CHARS || text.split("\n").length > 3;
	const kind = t(`steps.kind.${step.kind}`, { defaultValue: t("steps.kind.other") });
	return (
		<div className={cn("min-w-0 rounded-md border px-3 py-2", step.is_error && "border-chart-error/50")} data-testid="steps-sbs-step" data-seq={step.seq}>
			<div className="text-muted-foreground flex flex-wrap items-center gap-x-2 text-xs">
				<span className="text-foreground font-semibold md:hidden">{label}</span>
				<span className="font-medium">{kind}</span>
				{step.tool_name && <span className="font-mono">{step.tool_name}</span>}
				{typeof step.exit_code === "number" && <span className="font-mono">exit {step.exit_code}</span>}
				<span className="ml-auto font-mono tabular-nums">#{step.seq}</span>
			</div>
			{long ? (
				<details className="mt-1" data-testid="steps-sbs-fold">
					<summary className="cursor-pointer text-xs break-words">
						{text.slice(0, 120)}… <span className="text-primary">{t("steps.more")}</span>
					</summary>
					<pre className="mt-1 max-h-80 overflow-auto font-mono text-xs break-words whitespace-pre-wrap">{text}</pre>
				</details>
			) : (
				<p className="mt-1 font-mono text-xs break-words whitespace-pre-wrap">{text}</p>
			)}
		</div>
	);
}
