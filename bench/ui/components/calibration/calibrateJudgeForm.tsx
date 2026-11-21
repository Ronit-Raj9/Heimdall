import { JobRunButton } from "@/components/jobs/jobRunButton";
import { ModelPicker, type ModelRef, type ProviderId } from "@/components/models/modelPicker";
import { useSession } from "@/lib/session/sessionContext";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import type { CalibrationStatus, Job } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		calibrate: typeof import("@/locales/en/calibrate.json");
	}
}

/** Mirrors the server caps in cmd/bifrost-bench/jobs_cli_calibrate.go. */
export const HOSTED_CAP_CALLS = 400;
export const HOSTED_CAP_USD = 5;
/** First pass, repeat pass and two reworded prompts per item, each with the judge's votes. */
export const PASSES_PER_ITEM = 4;

interface Rate {
	value?: number | null;
}
interface Stats {
	agreement?: Rate;
	fpr?: Rate;
	kappa?: number | null;
}
/** The calibrate.run job result (calibrateResult in the Go job kind). */
export interface CalibrateResult {
	suite: string;
	estimate: boolean;
	provider: string;
	model: string;
	items: number;
	calls: number;
	forecast_usd?: number;
	price_note?: string;
	spent_usd: number;
	record_id?: string;
	human_labels?: number;
	seed_items?: number;
	human?: Stats;
	seed?: Stats;
}

/** Key store provider -> judge provider, as judgeKeyProvider on the server. */
function judgeProvider(p: string): string {
	return p === "openai-compatible" ? "openai" : p;
}

const pct = (r: Rate | undefined, none: string) => (r?.value == null ? none : `${(r.value * 100).toFixed(1)}%`);

/**
 * "Calibrate judge" on a suite's Calibration tab: pick the judge model (the suite's pinned one by
 * default) and a sample size, see the forecast, run the calibrate.run job with its live log and
 * read agreement and kappa once it ends. Hosted runs pay with the org's own key under a hard cap.
 */
export function CalibrateJudgeForm({ status, onDone }: { status: CalibrationStatus; onDone?(): void }) {
	const { t } = useTranslation("calibrate");
	const session = useSession();
	const hosted = session.authMode === "hosted";
	const models = useGetModelsQuery();
	const pinned = status.provider && status.model ? ({ provider: status.provider as ProviderId, model: status.model } as ModelRef) : null;
	const [model, setModel] = useState<ModelRef | null>(pinned);
	const [sample, setSample] = useState(0);
	const [seed, setSeed] = useState(false);
	const [forecast, setForecast] = useState<CalibrateResult | null>(null);
	const [result, setResult] = useState<CalibrateResult | null>(null);

	const votes = status.latest?.votes ?? 1;
	const labels = status.human_labels + status.seed_items;
	const items = sample > 0 ? Math.min(sample, labels) : labels;
	const calls = items * votes * PASSES_PER_ITEM;
	const provider = model?.provider ?? (status.provider as ProviderId | undefined) ?? "openai";
	const keys = models.data?.keys ?? [];
	const hasKey = keys.some((k) => judgeProvider(k.provider) === judgeProvider(provider) && k.status !== "invalid" && !k.over_budget);
	const noKey = hosted && models.isSuccess && !hasKey;

	const params = {
		suite: status.suite,
		...(model ? { provider: judgeProvider(model.provider), model: model.model } : {}),
		...(model?.keyId ? { key_id: model.keyId } : {}),
		...(sample > 0 ? { sample } : {}),
		...(seed ? { seed_controls: true } : {}),
	};
	const onEstimate = useCallback((job: Job) => {
		if (job.status === "succeeded" && job.result) setForecast(job.result as unknown as CalibrateResult);
	}, []);
	const onRun = useCallback(
		(job: Job) => {
			if (job.status === "succeeded" && job.result) setResult(job.result as unknown as CalibrateResult);
			onDone?.();
		},
		[onDone],
	);

	if (!status.judged) {
		return (
			<section className="border-border flex flex-col gap-2 rounded-lg border p-4" data-testid="calibrate-judge">
				<h3 className="text-sm font-semibold">{t("title")}</h3>
				<p className="text-muted-foreground text-sm">{t("notJudged")}</p>
				<Link to="/files" className="text-primary text-sm underline" data-testid="calibrate-judge-files">
					{t("editSuite")}
				</Link>
			</section>
		);
	}

	return (
		<section className="border-border flex min-w-0 flex-col gap-3 rounded-lg border p-4" data-testid="calibrate-judge">
			<h3 className="text-sm font-semibold">{t("title")}</h3>
			<p className="text-muted-foreground text-sm">{t("intro")}</p>
			<div className="flex flex-col gap-1">
				<span className="text-xs font-medium">{t("model")}</span>
				<ModelPicker value={model} onChange={setModel} purpose="judge" testId="calibrate-judge-model" />
			</div>
			<label className="flex flex-col gap-1 text-xs font-medium">
				{t("sample")}
				<input
					type="number"
					min={0}
					max={500}
					value={sample}
					onChange={(e) => setSample(Math.max(0, Math.min(500, Math.floor(Number(e.target.value) || 0))))}
					className="border-input bg-background h-8 w-28 rounded-md border px-2 text-sm"
					data-testid="calibrate-judge-sample"
				/>
				<span className="text-muted-foreground font-normal">{t("sampleHint", { count: labels })}</span>
			</label>
			<label className="flex items-center gap-2 text-xs">
				<input type="checkbox" checked={seed} onChange={(e) => setSeed(e.target.checked)} data-testid="calibrate-judge-seed" />
				{t("seedControls")}
			</label>
			<p className="text-sm" data-testid="calibrate-judge-calls">
				{t("approxCalls", { calls })}
				{hosted && <span className="text-muted-foreground"> {t("hostedCap", { calls: HOSTED_CAP_CALLS, usd: HOSTED_CAP_USD })}</span>}
			</p>
			{labels === 0 && !seed && <p className="text-muted-foreground text-sm">{t("noLabels")}</p>}
			{noKey ? (
				<p className="text-sm" data-testid="calibrate-judge-nokey">
					{t("noKey", { provider })}{" "}
					<Link to="/models" search={{ add: provider } as never} className="text-primary underline" data-testid="calibrate-judge-addkey">
						{t("addKey")}
					</Link>
				</p>
			) : (
				<div className="flex flex-col gap-3">
					<JobRunButton kind="calibrate.run" params={{ ...params, estimate: true }} label={t("estimate")} variant="outline" onDone={onEstimate} />
					{forecast && (
						<p className="text-sm" data-testid="calibrate-judge-forecast">
							{t("forecast", {
								calls: forecast.calls,
								items: forecast.items,
								usd: forecast.forecast_usd != null ? `$${forecast.forecast_usd.toFixed(4)}` : t("unpriced", { note: forecast.price_note ?? "" }),
							})}
						</p>
					)}
					<JobRunButton
						kind="calibrate.run"
						params={{ ...params, yes: true }}
						label={t("run")}
						confirm={t("confirm", { calls: forecast?.calls ?? calls })}
						onDone={onRun}
					/>
				</div>
			)}
			{result && <CalibrateResultView result={result} />}
		</section>
	);
}

/** Agreement, kappa and false-positive rate of a finished calibration. */
export function CalibrateResultView({ result }: { result: CalibrateResult }) {
	const { t } = useTranslation("calibrate");
	const none = t("none");
	const rows: [string, string][] = [
		[t("agreement"), pct(result.human?.agreement, none)],
		[t("kappa"), result.human?.kappa == null ? none : result.human.kappa.toFixed(3)],
		[t("fpr"), pct(result.human?.fpr, none)],
		[t("humanLabels"), String(result.human_labels ?? 0)],
		...(result.seed ? ([[t("seedAgreement"), pct(result.seed.agreement, none)]] as [string, string][]) : []),
		[t("spent"), `$${result.spent_usd.toFixed(4)}`],
	];
	return (
		<div className="flex flex-col gap-2" data-testid="calibrate-judge-result">
			<h4 className="text-sm font-semibold">
				{t("result")}
				{result.record_id && <span className="text-muted-foreground ml-2 font-mono text-xs">{t("record", { id: result.record_id })}</span>}
			</h4>
			<dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
				{rows.map(([k, v]) => (
					<div key={k} className="contents">
						<dt className="text-muted-foreground">{k}</dt>
						<dd className="font-mono">{v}</dd>
					</div>
				))}
			</dl>
		</div>
	);
}
