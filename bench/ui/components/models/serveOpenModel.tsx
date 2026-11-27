import { type DestructiveConfirmOptions, useConfirm } from "@/components/platform/confirmDialog";
import { InlineError } from "@/components/runs/screenStates";
import { PaidButton } from "@/components/ui/paidButton";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { notify } from "@/lib/notify";
import "@/components/suites/uf6bI18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useListWorkerPlatformsQuery } from "@/lib/store/apis/workerPlatformsApi";
import { canUse, useSession } from "@/lib/session/sessionContext";
import {
	activeDeployments,
	defaultGPU,
	maxCost,
	type OpenModelDeployment,
	useGetOpenModelCatalogQuery,
	useListOpenModelsQuery,
	useStartOpenModelMutation,
	useStopOpenModelMutation,
} from "@/lib/store/apis/openModelsApi";
import { Server } from "lucide-react";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

/** A time limit the server can bound: 0.1 to 24 hours (M8-15; 0 used to mean no limit). */
export function hoursProblem(limit: number): boolean {
	return !Number.isFinite(limit) || limit < 0.1 || limit > 24;
}

/** Radix Select has no empty value: this stands for "not tied to an evaluation". */
const NO_EVALUATION = "__none__";

/** Q-53: the empty list says "connect Modal first" only while Modal is not connected. */
export function serveEmptyKey(modalConnected: boolean): "serve.empty" | "serve.emptyReady" {
	return modalConnected ? "serve.emptyReady" : "serve.empty";
}

/** M8-15: only an unfinished evaluation can stop a server, so only those are offered. */
export function openEvaluations<T extends { state?: string }>(list: readonly T[]): T[] {
	return list.filter((x) => x.state !== "done" && x.state !== "failed" && x.state !== "cancelled");
}

const STATE_TONE: Record<string, "success" | "warning" | "outline"> = {
	ready: "success",
	starting: "outline",
	stopping: "warning",
	failed: "warning",
	stopped: "outline",
};

/**
 * Serve an open model on the org's own Modal account (lane Z05): model, engine, GPU and a time
 * limit; the running ones with the model name to put in an agent config and a stop button. Hosted
 * only: local mode has no keystore for the per-deployment key.
 */
export function ServeOpenModel() {
	const { t } = useTranslation("models");
	const session = useSession();
	const hosted = session.authMode === "hosted";
	const isAdmin = canUse(session, "admin");
	const catalog = useGetOpenModelCatalogQuery(undefined, { skip: !hosted });
	const list = useListOpenModelsQuery(undefined, { skip: !hosted, pollingInterval: 10000, skipPollingIfUnfocused: true });
	const [start, started] = useStartOpenModelMutation();
	const [stop] = useStopOpenModelMutation();
	const models = catalog.data?.models ?? [];
	const engines = catalog.data?.engines ?? [];
	const [modelId, setModelId] = useState("");
	const [engine, setEngine] = useState("vllm");
	const [gpu, setGPU] = useState("");
	const [hours, setHours] = useState("2");
	const [expId, setExpId] = useState("");
	const [error, setError] = useState<unknown>(null);
	const [stopError, setStopError] = useState<unknown>(null);
	// M8-15: the evaluation is picked from a list, not typed; only unfinished ones can stop a server.
	const experiments = useListExperimentsQuery(undefined, { skip: !hosted || !isAdmin });
	const confirm = useConfirm();
	const { t: tu } = useTranslation("uf6b");
	const platforms = useListWorkerPlatformsQuery(undefined, { skip: !hosted });
	const modalConnected = (platforms.data?.platforms ?? []).some((p) => p.name === "modal" && p.connected);
	if (!hosted) return null;

	const model = models.find((m) => m.id === modelId) ?? models[0];
	const gpuName = gpu && model?.gpus.some((g) => g.name === gpu && g.count > 0) ? gpu : defaultGPU(model);
	const choice = model?.gpus.find((g) => g.name === gpuName);
	const limit = Number(hours);
	const deployments = list.data?.deployments ?? [];
	const running = activeDeployments(deployments);

	// M8-15: starting a GPU bills the org's Modal account, so the shared paid button asks first,
	// with the hours and the most it can cost.
	const startServing = async () => {
		if (!model || hoursProblem(limit)) return;
		setError(null);
		try {
			await start({
				model: model.id,
				engine,
				gpu: gpuName,
				max_hours: limit,
				experiment_id: expId.trim() || undefined,
			}).unwrap();
			notify({ text: t("started", { ns: "toast", name: model.title }) });
		} catch (err) {
			setError(err);
		}
	};
	// X-70, M1-44: Stop ends the server at once, so it asks first; a failed stop says why.
	const stopServing = async (d: OpenModelDeployment) => {
		const name = d.model_name || d.title;
		const opts: DestructiveConfirmOptions = { destructive: true, title: tu("serve.stopTitle", { model: name }), confirmLabel: t("serve.stop") };
		if (!(await confirm(tu("confirm.serveStop", { model: name }), opts))) return;
		setStopError(null);
		try {
			await stop(d.id).unwrap();
			notify({ text: t("stopped", { ns: "toast", name }) });
		} catch (err) {
			setStopError(err);
		}
	};
	const gpuLabel = (g: NonNullable<typeof choice>) =>
		`${g.count > 1 ? `${g.count} x ` : ""}${g.title} · $${g.usd_per_hour_total.toFixed(2)}/h${g.recommended ? ` · ${t("serve.recommended")}` : ""}`;
	const openExperiments = openEvaluations(experiments.data?.experiments ?? []);

	return (
		<section className="flex flex-col gap-3" data-testid="serve-open-model">
			<div className="flex flex-col gap-0.5">
				<h2 className="flex items-center gap-2 text-lg font-semibold">
					<Server className="size-4" aria-hidden="true" />
					{t("serve.title")}
				</h2>
				<p className="text-muted-foreground text-sm">{t("serve.subtitle")}</p>
			</div>
			{isAdmin && models.length > 0 && (
				<form className="bg-card grid min-w-0 grid-cols-1 gap-3 rounded-xl border px-4 py-4 sm:grid-cols-2 sm:px-5" onSubmit={(e: FormEvent) => e.preventDefault()} data-testid="serve-form">
					<div className="flex min-w-0 flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs" id="serve-model-label">
							{t("serve.model")}
						</span>
						<Select
							value={model?.id ?? ""}
							onValueChange={(v) => {
								setModelId(v);
								setGPU("");
							}}
						>
							<SelectTrigger className="w-full min-w-0" aria-labelledby="serve-model-label" data-testid="serve-model">
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								{models.map((m) => (
									<SelectItem key={m.id} value={m.id}>
										{m.title} ({m.licence}
										{m.gated ? `, ${t("serve.gated")}` : ""})
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
					<div className="flex min-w-0 flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs" id="serve-engine-label">
							{t("serve.engine")}
						</span>
						<Select value={engine} onValueChange={setEngine}>
							<SelectTrigger className="w-full min-w-0" aria-labelledby="serve-engine-label" data-testid="serve-engine">
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								{engines.map((en) => (
									<SelectItem key={en.id} value={en.id}>
										{en.title} {en.version}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
					<div className="flex min-w-0 flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs" id="serve-gpu-label">
							{t("serve.gpu")}
						</span>
						<Select value={gpuName} onValueChange={setGPU}>
							<SelectTrigger className="w-full min-w-0" aria-labelledby="serve-gpu-label" data-testid="serve-gpu">
								<SelectValue>{choice ? gpuLabel(choice) : null}</SelectValue>
							</SelectTrigger>
							<SelectContent>
								{(model?.gpus ?? []).map((g) => (
									<SelectItem key={g.name} value={g.name} disabled={g.count === 0} disabledReason={g.unsupported} title={g.unsupported}>
										{gpuLabel(g)}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					</div>
					<label className="flex min-w-0 flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs">{t("serve.hours")}</span>
						<Input
							type="number"
							min="0.1"
							max="24"
							step="0.5"
							required
							value={hours}
							onChange={(e) => setHours(e.target.value)}
							aria-invalid={hoursProblem(limit) || undefined}
							data-testid="serve-hours"
						/>
						{hoursProblem(limit) && (
							<span className="text-destructive text-xs" data-testid="serve-hours-problem">
								{tu("serve.hoursRange")}
							</span>
						)}
					</label>
					<div className="flex min-w-0 flex-col gap-1 text-sm sm:col-span-2">
						<span className="text-muted-foreground text-xs" id="serve-exp-label">
							{t("serve.evaluation")}
						</span>
						<Select value={expId || NO_EVALUATION} onValueChange={(v) => setExpId(v === NO_EVALUATION ? "" : v)}>
							<SelectTrigger className="w-full min-w-0" aria-labelledby="serve-exp-label" data-testid="serve-exp">
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value={NO_EVALUATION}>{tu("serve.noEvaluation")}</SelectItem>
								{openExperiments.map((x) => (
									<SelectItem key={x.exp_id} value={x.exp_id}>
										{x.name || x.exp_id}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
						<span className="text-muted-foreground text-xs">{t("serve.evaluationHint")}</span>
					</div>
					<div className="flex flex-wrap items-center gap-3 sm:col-span-2">
						<PaidButton
							size="sm"
							verb={t("serve.start")}
							capUsd={maxCost(choice, limit)}
							confirm="always"
							kind="form"
							confirmText={tu("confirm.serveStart", { hours: limit, max: maxCost(choice, limit).toFixed(2) })}
							disabled={started.isLoading || !choice || choice.count === 0 || hoursProblem(limit)}
							isLoading={started.isLoading}
							onConfirm={startServing}
							testId="serve-start"
						/>
						<span className="text-muted-foreground min-w-0 text-xs break-words" data-testid="serve-cost">
							{t("serve.cost", { max: maxCost(choice, limit).toFixed(2) })}
						</span>
						<InlineError error={error} onRetry={() => void startServing()} testId="serve-error" className="sm:col-span-2" />
					</div>
				</form>
			)}
			<InlineError error={stopError} testId="serve-stop-error" />
			{deployments.length === 0 ? (
				<p className="text-muted-foreground text-sm" data-testid="serve-empty">
					{t(serveEmptyKey(modalConnected))}
				</p>
			) : (
				<ul className="bg-card flex flex-col divide-y rounded-xl border" data-testid="serve-list">
					{deployments.slice(0, 20).map((d) => (
						<DeploymentRow
							key={d.id}
							d={d}
							canStop={isAdmin && running.includes(d)}
							onStop={() => void stopServing(d)}
						/>
					))}
				</ul>
			)}
		</section>
	);
}

function DeploymentRow({ d, canStop, onStop }: { d: OpenModelDeployment; canStop: boolean; onStop: () => void }) {
	const { t } = useTranslation("models");
	return (
		<li className="flex flex-col gap-1.5 px-5 py-3 sm:flex-row sm:items-start sm:gap-4" data-testid={`serve-row-${d.id}`}>
			<div className="flex min-w-0 flex-1 flex-col gap-1">
				<div className="flex flex-wrap items-center gap-2">
					<span className="text-sm font-medium">{d.title}</span>
					<Badge variant={STATE_TONE[d.state] ?? "outline"} className="rounded-full px-2" data-testid={`serve-state-${d.id}`}>
						{t(`serve.states.${d.state}`, { defaultValue: d.state })}
					</Badge>
					<span className="text-muted-foreground text-xs">
						{d.gpu_count > 1 ? `${d.gpu_count} x ` : ""}
						{d.gpu} · {d.engine} · {t("serve.maxCost", { max: d.max_usd.toFixed(2) })}
					</span>
				</div>
				{d.state === "ready" && (
					<span className="text-muted-foreground text-xs">
						{t("serve.useAs")} <code className="font-mono text-[11px] break-all">{d.model_name}</code>
					</span>
				)}
				{d.experiment_id && <span className="text-muted-foreground text-xs">{t("serve.forEvaluation", { id: d.experiment_id })}</span>}
				{d.detail && <span className="text-muted-foreground text-xs">{d.detail}</span>}
			</div>
			{canStop && (
				<Button size="sm" variant="outline" onClick={onStop} data-testid={`serve-stop-${d.id}`}>
					{t("serve.stop")}
				</Button>
			)}
		</li>
	);
}
