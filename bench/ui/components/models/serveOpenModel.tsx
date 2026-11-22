import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
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
	const [error, setError] = useState<string | null>(null);
	if (!hosted) return null;

	const model = models.find((m) => m.id === modelId) ?? models[0];
	const gpuName = gpu && model?.gpus.some((g) => g.name === gpu && g.count > 0) ? gpu : defaultGPU(model);
	const choice = model?.gpus.find((g) => g.name === gpuName);
	const limit = Number(hours);
	const deployments = list.data?.deployments ?? [];
	const running = activeDeployments(deployments);

	const onSubmit = async (e: FormEvent) => {
		e.preventDefault();
		if (!model) return;
		setError(null);
		try {
			await start({
				model: model.id,
				engine,
				gpu: gpuName,
				max_hours: limit > 0 ? limit : undefined,
				experiment_id: expId.trim() || undefined,
			}).unwrap();
		} catch (err) {
			setError(getBenchErrorMessage(err));
		}
	};

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
				<form className="bg-card grid gap-3 rounded-xl border px-5 py-4 sm:grid-cols-2" onSubmit={onSubmit} data-testid="serve-form">
					<label className="flex flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs">{t("serve.model")}</span>
						<select
							className="bg-background h-9 rounded-md border px-2"
							value={model?.id ?? ""}
							onChange={(e) => {
								setModelId(e.target.value);
								setGPU("");
							}}
							data-testid="serve-model"
						>
							{models.map((m) => (
								<option key={m.id} value={m.id}>
									{m.title} ({m.licence}
									{m.gated ? `, ${t("serve.gated")}` : ""})
								</option>
							))}
						</select>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs">{t("serve.engine")}</span>
						<select className="bg-background h-9 rounded-md border px-2" value={engine} onChange={(e) => setEngine(e.target.value)} data-testid="serve-engine">
							{engines.map((en) => (
								<option key={en.id} value={en.id}>
									{en.title} {en.version}
								</option>
							))}
						</select>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs">{t("serve.gpu")}</span>
						<select className="bg-background h-9 rounded-md border px-2" value={gpuName} onChange={(e) => setGPU(e.target.value)} data-testid="serve-gpu">
							{(model?.gpus ?? []).map((g) => (
								<option key={g.name} value={g.name} disabled={g.count === 0} title={g.unsupported}>
									{g.count > 1 ? `${g.count} x ` : ""}
									{g.title} · ${g.usd_per_hour_total.toFixed(2)}/h{g.recommended ? ` · ${t("serve.recommended")}` : ""}
								</option>
							))}
						</select>
					</label>
					<label className="flex flex-col gap-1 text-sm">
						<span className="text-muted-foreground text-xs">{t("serve.hours")}</span>
						<Input type="number" min="0.1" max="24" step="0.5" value={hours} onChange={(e) => setHours(e.target.value)} data-testid="serve-hours" />
					</label>
					<label className="flex flex-col gap-1 text-sm sm:col-span-2">
						<span className="text-muted-foreground text-xs">{t("serve.evaluation")}</span>
						<Input value={expId} placeholder={t("serve.evaluationHint")} onChange={(e) => setExpId(e.target.value)} data-testid="serve-exp" />
					</label>
					<div className="flex flex-wrap items-center gap-3 sm:col-span-2">
						<Button type="submit" size="sm" disabled={started.isLoading || !choice || choice.count === 0} data-testid="serve-start">
							{t("serve.start")}
						</Button>
						<span className="text-muted-foreground text-xs" data-testid="serve-cost">
							{t("serve.cost", { max: maxCost(choice, limit).toFixed(2) })}
						</span>
						{error && (
							<span className="text-destructive text-xs" role="alert" data-testid="serve-error">
								{error}
							</span>
						)}
					</div>
				</form>
			)}
			{deployments.length === 0 ? (
				<p className="text-muted-foreground text-sm" data-testid="serve-empty">
					{t("serve.empty")}
				</p>
			) : (
				<ul className="bg-card flex flex-col divide-y rounded-xl border" data-testid="serve-list">
					{deployments.slice(0, 20).map((d) => (
						<DeploymentRow key={d.id} d={d} canStop={isAdmin && running.includes(d)} onStop={() => void stop(d.id)} />
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
						{t("serve.useAs")} <code className="font-mono text-[11px]">{d.model_name}</code>
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
